/*
Package pngutil provides a simple way to handle some common
tasks with PNGs such as replacing metadata and checking magic
bytes.
*/
package pngutil

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ihdrEnd int64 = 33 // the offset at which the IHDR chunk ends

var (
	header = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	ihdr   = []byte{0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	iend   = []byte{0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}
	itxt   = []byte("iTXt")

	/*
		Gap between keyword and text for iTXt chunk.
		    Null separator
		    Compression flag
		    Compression method
		    (Omitted language tag)
		    Null separator
		    (Omitted translated keyword)
		    Null separator
		Each of these may be set to zero.
	*/
	itxtKWGap = []byte{0x00, 0x00, 0x00, 0x00, 0x00}
)

/*
Assert returns an error if r doesn't represent
a valid PNG image. It checks for the header, the
first 8 bytes of the IHDR chunk, and the IEND
chunk without reading the entire file.

The current offset of rs is restored after Assert
has completed its checks.
*/
func Assert(rs io.ReadSeeker) (err error) {

	/*
		Return seek offset to current position
		after we're done with it.
	*/
	offset, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	defer func() {
		if _, sErr := rs.Seek(offset, io.SeekStart); sErr != nil {
			err = fmt.Errorf("pngutil: %w: %s", err, sErr.Error())
		}
	}()

	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return err
	}

	p := make([]byte, 16, 16)
	if _, err = rs.Read(p); err != nil {
		return err
	}

	if !bytes.Equal(p, append(header, ihdr...)) {
		return errors.New("pngutil: missing header or IHDR chunk")
	}

	if _, err = rs.Seek(-12, io.SeekEnd); err != nil {
		return err
	}

	if _, err = rs.Read(p); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	if !bytes.Equal(p[0:12], iend) {
		return errors.New("pngutil: missing IEND chunk at end of file")
	}

	return nil
}

/*
Predefined metadata keywords in the PNG specification:
https://www.w3.org/TR/PNG/#11keywords
*/
const (
	MetaTitle        = "Title"         // Short (one line) title or caption for image
	MetaAuthor       = "Author"        // Name of image's creator
	MetaDescription  = "Description"   // Description of image (possibly long)
	MetaCopyright    = "Copyright"     // Copyright notice
	MetaCreationTime = "Creation Time" // Time of original image creation
	MetaSoftware     = "Software"      // Software used to create the image
	MetaDisclaimer   = "Disclaimer"    // Legal disclaimer
	MetaWarning      = "Warning"       // Warning of nature of content
	MetaSource       = "Source"        // Device used to create the image
	MetaComment      = "Comment"       // Miscellaneous comment
)

type Metadata map[string]string

/*
ReplaceMeta takes a PNG file represented by f and returns
a readseeker mrs which is the same file with only the supplied
metadata. The resulting image represented by mrs is not altered.

A zero-length metadata will result in mrs having no metadata at all.

ReplaceMeta calls Assert and will error under the same conditions.
It is unnecessary for callers to call Assert if they intend to
immediately follow with ReplaceMeta.

Since mrs is a wrapper around the new metadata and f, altering
f will affect mrs. Therefore callers are recommended to drain
mrs before altering f.

The metadata is assigned to an iTXt chunk at the start of the
file.
*/
func ReplaceMeta(f io.ReadSeeker, metadata Metadata) (mrs *multiReadSeeker, err error) {

	if err = Assert(f); err != nil {
		return nil, err
	}

	// Pre-calculate length of our iTXt chunks.
	itxtLen := 0
	for k, v := range metadata {
		itxtLen += 4      // chunk length
		itxtLen += 4      // chunk type
		itxtLen += len(k) // keyword
		itxtLen += 5      // null separtors, compression flags, languages
		itxtLen += len(v) // text
		itxtLen += 4      // chunk CRC
	}

	/*
		Make byte slice of that length with 8
		bytes extra for scratch space below.
	*/
	bb := make([]byte, itxtLen+8)
	i := 0
	for k, v := range metadata {
		start := i                              // save start offset of this chunk
		i += 4                                  // skip length
		i += copy(bb[i:], itxt)                 // chunk type
		i += copy(bb[i:], k)                    // keyword
		i += 5                                  // skip null separators, compression flags, languages
		i += copy(bb[i:], v)                    // text
		length := uint32(i - (start + 8))       // calculate length
		int32ToBytes(bb[start:start+4], length) // add length
		crc := crc32.NewIEEE()
		crc.Write(bb[start+4 : start+8+int(length)]) // input chunk type + data
		int32ToBytes(bb[i:], crc.Sum32())            // calculate CRC
		i += 4                                       // add CRC length
	}

	// Alias scratch space at the end of the metadata buffer.
	p := bb[i:]

	// Seek to end of IHDR chunk (PNG 8 byte header, 13 byte IHDR chunk)
	if _, err = f.Seek(ihdrEnd, io.SeekStart); err != nil {
		return nil, err
	}
	readers := []*skipReadSeeker{
		&skipReadSeeker{
			name: "header",
			rs:   f,
			end:  ihdrEnd,
		},
		&skipReadSeeker{
			name: "metadata",
			rs:   bytes.NewReader(bb[:len(bb)-8]),
			end:  int64(len(bb) - 8),
		},
	}
	pos := ihdrEnd
	keptPrevChunk := false

	for {

		// Read next 8 bytes.
		n, err := f.Read(p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if n != 8 {
			return nil, errors.New("pngutil: couldn't read next chunk length and type")
		}

		length := int64(binary.BigEndian.Uint32(p[0:4])) + 4 // add 4 for CRC

		// Discard chunk.
		chunk := string(p[4:8])
		if !retain[chunk] {
			keptPrevChunk = false
			goto skip
		}

		// Concat this chunk to the previous.
		if keptPrevChunk {
			last := len(readers) - 1
			readers[last].end = pos
		} else { // Otherwise add new chunk.
			readers = append(readers, &skipReadSeeker{
				name:  "chunk",
				rs:    f,
				start: pos,
				end:   pos + length,
			})
		}
		keptPrevChunk = true

	skip:
		if pos, err = f.Seek(length, io.SeekCurrent); err != nil {
			return nil, err
		}
	}

	readers[len(readers)-1].end = pos

	return newMultiReadSeeker(readers...)
}

var retain = map[string]bool{
	"IHDR": true,
	"PLTE": true,
	"IDAT": true,
	"IEND": true,
}

func int32ToBytes(p []byte, n uint32) {
	binary.BigEndian.PutUint32(p, n)
}

func closeFile(c io.Closer, err *error) {
	cErr := c.Close()
	if err == nil {
		*err = cErr
		return
	}
	if cErr == nil {
		return
	}
	*err = fmt.Errorf("%w: %v", *err, cErr)
}

/*
WriteFile drains r and writes it to a new file
at name, returning the number of bytes it wrote
and an error, if any.

If name doesn't already end in ".png" WriteFile
will add it to the end.
*/
func WriteFile(name string, r io.Reader) (n int64, err error) {

	if ext := filepath.Ext(name); ext != ".png" {
		name = strings.TrimRight(name, ".")
		name += ".png"
	}

	name, err = filepath.Abs(name)
	if err != nil {
		return n, fmt.Errorf("pngutil: %w", err)
	}

	f, err := os.Create(name)
	if err != nil {
		return n, fmt.Errorf("pngutil: %w", err)
	}
	defer closeFile(f, &err)

	tr := io.TeeReader(r, f)
	p := make([]byte, 64, 64)

	for {
		count, err := tr.Read(p)
		n += int64(count)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("pngutil: %w", err)
		}
	}

	return n, nil
}
