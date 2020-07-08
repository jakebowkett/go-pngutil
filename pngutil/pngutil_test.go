package pngutil

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestSkipReadSeeker(t *testing.T) {

	cases := []struct {
		val    byte
		offset int64
		start  int64
		end    int64
		whence int
		err    bool
	}{
		{3, 1, 2, 6, io.SeekStart, false},
		{0, 9, 2, 6, io.SeekStart, true}, // EOF
		{5, -1, 2, 6, io.SeekEnd, false},
		{3, 1, 2, 6, io.SeekCurrent, false},
		{0, 1, -1, 6, io.SeekStart, true},
	}

	for _, c := range cases {

		var err error
		var seekN int64
		var readN int
		var whenceStr string
		errStr := "nil"
		if c.err {
			errStr = "error"
		}
		switch c.whence {
		case io.SeekStart:
			whenceStr = "start"
		case io.SeekCurrent:
			whenceStr = "current"
		case io.SeekEnd:
			whenceStr = "end"
		}
		p := make([]byte, 1)
		rs := bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7})
		srs := skipReadSeeker{
			rs:    rs,
			start: c.start,
			end:   c.end,
		}
		if seekN, err = srs.Seek(c.offset, c.whence); err != nil {
			if c.err {
				continue
			} else {
				goto logErr
			}
		}
		if readN, err = srs.Read(p); err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			} else {
				goto logErr
			}
		}
		if p[0] != c.val {
			err = errors.New("unexpected value")
			goto logErr
		}
		if err == nil {
			continue
		}
	logErr:
		t.Errorf("skipReadSeeker.Seek(%d, %s)\n"+
			"    have val: %d, seekN: %d,   readN: %d, err: %v\n"+
			"    want val: %d, seekN: n/a, readN: %d, err: %v\n",
			c.offset, whenceStr,
			p[0], seekN, readN, err,
			c.val, 1, errStr)
	}
}

func TestMultiReadSeeker(t *testing.T) {

	cases := []struct {
		offset int64
		read   int
		whence int
		err    bool
		val    []byte
	}{
		{6, 4, io.SeekStart, false, []byte{6, 7, 8, 9}},
		{20, 0, io.SeekStart, true, []byte{0, 0, 0, 0}}, // EOF
		{-2, 2, io.SeekEnd, true, []byte{14, 15, 0, 0}},
		{6, 4, io.SeekCurrent, false, []byte{6, 7, 8, 9}},
		{-1, 0, io.SeekStart, true, []byte{0, 0, 0, 0}},
	}

	for _, c := range cases {
		var err error
		var seekN int64
		var readN int
		var whenceStr string
		errStr := "nil"
		if c.err {
			errStr = "error"
		}
		switch c.whence {
		case io.SeekStart:
			whenceStr = "start"
		case io.SeekCurrent:
			whenceStr = "current"
		case io.SeekEnd:
			whenceStr = "end"
		}
		p := make([]byte, 4)
		mrs, err := newMultiReadSeeker(
			&skipReadSeeker{
				rs:  bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7}),
				end: 8,
			},
			&skipReadSeeker{
				rs:  bytes.NewReader([]byte{8, 9, 10, 11, 12, 13, 14, 15}),
				end: 8,
			},
		)
		if err != nil {
			goto logErr
		}
		if seekN, err = mrs.Seek(c.offset, c.whence); err != nil {
			if c.err {
				continue
			} else {
				goto logErr
			}
		}
		if readN, err = mrs.Read(p); err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			} else {
				goto logErr
			}
		}
		if !bytes.Equal(p, c.val) {
			err = errors.New("unexpected value")
			goto logErr
		}
		if err == nil {
			continue
		}
	logErr:
		t.Errorf("multiReadSeeker.Seek(%d, %s)\n"+
			"    have seekN: %3d, readN: %d, val: %v, err: %v\n"+
			"    want seekN: n/a, readN: %d, val: %v, err: %v\n",
			c.offset, whenceStr,
			seekN, readN, p, err,
			c.read, c.val, errStr)
	}
}
