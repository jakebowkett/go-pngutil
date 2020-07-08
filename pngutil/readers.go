package pngutil

import (
	"errors"
	"io"
)

/*
skipReadSeeker represents a view into a larger reader. It starts at
offset and ends at limit. Once it has read up to limit the Read
method returns an io.EOF error.

In this package all instances of skipReadSeeker use the same underlying
reader passed to ReplaceMeta.
*/
type skipReadSeeker struct {
	name  string
	rs    io.ReadSeeker
	start int64
	end   int64

	/*
	   offset is relative to start. That is,
	   it always begins at zero even if start
	   is not.
	*/
	offset int64
}

func (srs *skipReadSeeker) Read(p []byte) (n int, err error) {
	if srs.offset >= srs.end {
		return 0, io.EOF
	}
	toRead := srs.end - srs.offset
	if toRead > int64(len(p)) {
		toRead = int64(len(p))
	}
	n, err = srs.rs.Read(p[:toRead])
	srs.offset += int64(n)
	return n, err
}

func (srs *skipReadSeeker) Seek(offset int64, whence int) (n int64, err error) {
	switch whence {
	case io.SeekStart:
		offset += srs.start
	case io.SeekCurrent:
		offset += srs.start + srs.offset
	case io.SeekEnd:
		offset = srs.end + offset
	}
	if offset < srs.start {
		return n, errors.New("pngutil: skipReadSeeker seeking before start")
	}
	n, err = srs.rs.Seek(offset, io.SeekStart)
	srs.offset = offset
	return offset, err
}

type multiReadSeeker struct {
	overall     int64
	rsIdx       int
	readSeekers []*skipReadSeeker
	sizes       []int64
	size        int64
}

/*
Returns a new multireader that is a concatenation of
rs. All read seekers and the multireader itself will
be seeked to the start.
*/
func newMultiReadSeeker(readSeekers ...*skipReadSeeker) (mrs *multiReadSeeker, err error) {
	var sizes []int64
	var size int64
	for _, rs := range readSeekers {
		sz := rs.end - rs.start
		sizes = append(sizes, sz)
		size += sz
	}
	mrs = &multiReadSeeker{
		readSeekers: readSeekers,
		sizes:       sizes,
		size:        size,
	}
	/*
		ReplaceMeta uses its input read seeker to create
		two or more of the read seekers supplied to this
		function. Therefore we seek to the start here to
		ensure all readers are in the correct position.
	*/
	if _, err := mrs.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return mrs, nil
}

func (mrs *multiReadSeeker) Read(p []byte) (n int, err error) {

	read := 0
	for {
		if read == len(p) {
			break
		}
		n, err = mrs.readSeekers[mrs.rsIdx].Read(p[read:])
		read += n
		mrs.overall += int64(n)

		// If we reach the end of the current readseeker...
		if errors.Is(err, io.EOF) {

			// ...return if this is the last readseeker.
			if mrs.rsIdx == len(mrs.readSeekers)-1 {
				return read, err
			}

			/*
				Otherwise increment readseeker index, ensure
				said readseekers's cursor is at the start,
				then resume reading.
			*/
			mrs.rsIdx++
			_, _ = mrs.readSeekers[mrs.rsIdx].Seek(0, io.SeekStart)
			continue
		}

		// Immediately return on non-EOF error.
		if err != nil {
			return read, err
		}
	}

	return read, nil
}

func (mrs *multiReadSeeker) Seek(offset int64, whence int) (n int64, err error) {

	switch whence {
	case io.SeekStart:
		// to prevent default case
	case io.SeekCurrent:
		offset += mrs.overall
	case io.SeekEnd:
		offset = mrs.size + offset
	default:
		return 0, errors.New("pngutil: invalid whence value for multiReadSeeker")
	}

	var total int64
	for i, s := range mrs.sizes {
		if offset >= total && offset < total+s {
			mrs.rsIdx = i
			rsOffset := offset - total
			_, err := mrs.readSeekers[mrs.rsIdx].Seek(rsOffset, io.SeekStart)
			if err != nil {
				return 0, err
			}
			mrs.overall = offset
			return offset, nil
		}
		total += s
	}

	return 0, errors.New("pngutil: seek out of bounds for multiReadSeeker")
}

func (mrs *multiReadSeeker) Size() (n int64) {
	return mrs.size
}
