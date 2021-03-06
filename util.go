package main

import (
	"crypto/md5"
	"errors"
	"fmt"
	"github.com/cyfdecyf/bufio"
	"io"
	"net"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
)

const isWindows = runtime.GOOS == "windows"

type notification chan byte

func newNotification() notification {
	// Notification channle has size 1, so sending a single one will not block
	return make(chan byte, 1)
}

func (n notification) notify() {
	n <- 1
}

func (n notification) hasNotified() bool {
	select {
	case <-n:
		return true
	default:
		return false
	}
	return false
}

// ReadLine read till '\n' is found or encounter error. The returned line does
// not include ending '\r' and '\n'. If returns err != nil if and only if
// len(line) == 0.
func ReadLine(r *bufio.Reader) (string, error) {
	l, err := ReadLineSlice(r)
	return string(l), err
}

// ReadLineBytes read till '\n' is found or encounter error. The returned line
// does not include ending '\r\n' or '\n'. Returns err != nil if and only if
// len(line) == 0. Note the returned byte should not be used for append and
// maybe overwritten by next I/O operation. Copied code of readLineSlice from
// $GOROOT/src/pkg/net/textproto/reader.go
func ReadLineSlice(r *bufio.Reader) (line []byte, err error) {
	for {
		l, more, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		// Avoid the copy if the first call produced a full line.
		if line == nil && !more {
			return l, nil
		}
		line = append(line, l...)
		if !more {
			break
		}
	}
	return line, nil
}

func ASCIIToUpperInplace(b []byte) {
	for i := 0; i < len(b); i++ {
		if 97 <= b[i] && b[i] <= 122 {
			b[i] -= 32
		}
	}
}

func ASCIIToUpper(b []byte) []byte {
	buf := make([]byte, len(b))
	copy(buf, b)
	ASCIIToUpperInplace(buf)
	return buf
}

func ASCIIToLowerInplace(b []byte) {
	for i := 0; i < len(b); i++ {
		if 65 <= b[i] && b[i] <= 90 {
			b[i] += 32
		}
	}
}

func ASCIIToLower(b []byte) []byte {
	buf := make([]byte, len(b))
	copy(buf, b)
	ASCIIToLowerInplace(buf)
	return buf
}

func IsDigit(b byte) bool {
	return '0' <= b && b <= '9'
}

var spaceTbl = [256]bool{
	'\t': true, // ht
	'\n': true, // lf
	'\r': true, // cr
	' ':  true, // sp
}

func IsSpace(b byte) bool {
	return spaceTbl[b]
}

func TrimSpace(s []byte) []byte {
	if len(s) == 0 {
		return s
	}
	st := 0
	end := len(s) - 1
	for ; st < len(s) && IsSpace(s[st]); st++ {
	}
	if st == len(s) {
		return s[:0]
	}
	for ; end >= 0 && IsSpace(s[end]); end-- {
	}
	return s[st : end+1]
}

// FieldsN is simliar with bytes.Fields, but only consider space and '\t' as
// space, and will include all content in the final slice with ending white
// space characters trimmed. bytes.Split can't split on both space and '\t',
// and considers two separator as an empty item. bytes.FieldsFunc can't
// specify how much fields we need, which is required for parsing response
// status line. Returns nil if n < 0.
func FieldsN(s []byte, n int) [][]byte {
	if n <= 0 {
		return nil
	}
	res := make([][]byte, n)
	na := 0
	fieldStart := -1
	var i int
	for ; i < len(s); i++ {
		issep := s[i] == ' ' || s[i] == '\t'
		if fieldStart < 0 && !issep {
			fieldStart = i
		}
		if fieldStart >= 0 && issep {
			if na == n-1 {
				break
			}
			res[na] = s[fieldStart:i]
			na++
			fieldStart = -1
		}
	}
	if fieldStart >= 0 { // must have na <= n-1 here
		res[na] = TrimSpace(s[fieldStart:])
		if len(res[na]) != 0 { // do not consider ending space as a field
			na++
		}
	}
	return res[:na]
}

var digitTbl = [256]int8{
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, -1, -1, -1, -1, -1, -1,
	-1, 10, 11, 12, 13, 14, 15, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, 10, 11, 12, 13, 14, 15, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
}

// ParseIntFromBytes parse hexidecimal number from given bytes.
// No prefix (e.g. 0xdeadbeef) should given.
// base can only be 10 or 16.
func ParseIntFromBytes(b []byte, base int) (n int64, err error) {
	// Currently, we have to convert []byte to string to use strconv
	// Refer to: http://code.google.com/p/go/issues/detail?id=2632
	// That's why I created this function.
	if base != 10 && base != 16 {
		err = errors.New(fmt.Sprintf("Invalid base: %d\n", base))
		return
	}
	if len(b) == 0 {
		err = errors.New("Parse int from empty string")
		return
	}

	neg := false
	if b[0] == '+' {
		b = b[1:]
	} else if b[0] == '-' {
		b = b[1:]
		neg = true
	}

	for _, d := range b {
		v := digitTbl[d]
		if v == -1 {
			n = 0
			err = errors.New(fmt.Sprintf("Invalid number: %s", b))
			return
		}
		if int(v) >= base {
			n = 0
			err = errors.New(fmt.Sprintf("Invalid base %d number: %s", base, b))
			return
		}
		n *= int64(base)
		n += int64(v)
	}
	if neg {
		n = -n
	}
	return
}

func isFileExists(path string) (bool, error) {
	stat, err := os.Stat(path)
	if err == nil {
		if stat.Mode()&os.ModeType == 0 {
			return true, nil
		}
		return false, errors.New(path + " exists but is not regular file")
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isDirExists(path string) (bool, error) {
	stat, err := os.Stat(path)
	if err == nil {
		if stat.IsDir() {
			return true, nil
		}
		return false, errors.New(path + " exists but is not directory")
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Get host IP address
func hostIP() (addrs []string, err error) {
	name, err := os.Hostname()
	if err != nil {
		fmt.Printf("Error get host name: %v\n", err)
		return
	}

	addrs, err = net.LookupHost(name)
	if err != nil {
		fmt.Printf("Error getting host IP address: %v\n", err)
		return
	}
	return
}

func getUserHomeDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		fmt.Println("HOME environment variable is empty")
	}
	return home
}

func expandTilde(pth string) string {
	if len(pth) > 0 && pth[0] == '~' {
		home := getUserHomeDir()
		return path.Join(home, pth[1:])
	}
	return pth
}

// copyN copys N bytes from src to dst, reading at most rdSize for each read.
// rdSize should be smaller than the buffer size of Reader.
// Returns any encountered error.
func copyN(dst io.Writer, src *bufio.Reader, n, rdSize int) (err error) {
	// Most of the copy is copied from io.Copy
	for n > 0 {
		var b []byte
		var er error
		if n > rdSize {
			b, er = src.ReadN(rdSize)
		} else {
			b, er = src.ReadN(n)
		}
		nr := len(b)
		n -= nr
		if nr > 0 {
			nw, ew := dst.Write(b)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			err = er
			break
		}
	}
	return err
}

// copyNWithBuf copys N bytes from src to dst, using the specified buf as buffer. pre and
// end are written to w before and after the n bytes. copyN will try to
// minimize number of writes.
// No longer used now.
func copyNWithBuf(dst io.Writer, src io.Reader, n int, buf, pre, end []byte) (err error) {
	// XXX well, this is complicated in order to save writes
	var nn int
	bufLen := len(buf)
	var b []byte
	for n != 0 {
		if pre != nil {
			if len(pre) >= bufLen {
				// pre is larger than bufLen, can't save write operation here
				if _, err = dst.Write(pre); err != nil {
					return
				}
				pre = nil
				continue
			}
			// append pre to buf to save one write
			copy(buf, pre)
			if len(pre)+n < bufLen {
				// only need to read n bytes
				b = buf[len(pre) : len(pre)+n]
			} else {
				b = buf[len(pre):]
			}
		} else {
			if n < bufLen {
				b = buf[:n]
			} else {
				b = buf
			}
		}
		if nn, err = src.Read(b); err != nil {
			return
		}
		n -= nn
		if pre != nil {
			// nn is how much we need to write next
			nn += len(pre)
			pre = nil
		}
		// see if we can append end in buffer to save one write
		if n == 0 && end != nil && nn+len(end) <= bufLen {
			copy(buf[nn:], end)
			nn += len(end)
			end = nil
		}
		if _, err = dst.Write(buf[:nn]); err != nil {
			return
		}
	}
	if end != nil {
		if _, err = dst.Write(end); err != nil {
			return
		}
	}
	return
}

func md5sum(ss ...string) string {
	h := md5.New()
	for _, s := range ss {
		io.WriteString(h, s)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// only handles IPv4 address now
func hostIsIP(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, i := range parts {
		if len(i) == 0 || len(i) > 3 {
			return false
		}
		n, err := strconv.Atoi(i)
		if err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

// NetNbitIPv4Mask returns a IPMask with highest n bit set.
func NewNbitIPv4Mask(n int) net.IPMask {
	if n > 32 {
		panic("NewNbitIPv4Mask: bit number > 32")
	}
	mask := []byte{0, 0, 0, 0}
	for id := 0; id < 4; id++ {
		if n >= 8 {
			mask[id] = 0xff
		} else {
			mask[id] = ^byte(1<<(uint8(8-n)) - 1)
			break
		}
		n -= 8
	}
	return net.IPMask(mask)
}

var topLevelDomain = map[string]bool{
	"ac":  true,
	"co":  true,
	"org": true,
	"com": true,
	"net": true,
	"edu": true,
}

func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}

// host2Domain returns the domain of a host. It will recognize domains like
// google.com.hk. Returns empty string for simple host.
func host2Domain(host string) (domain string) {
	host, _ = splitHostPort(host)
	if hostIsIP(host) {
		return ""
	}
	host = trimLastDot(host)
	lastDot := strings.LastIndex(host, ".")
	if lastDot == -1 {
		return ""
	}
	// Find the 2nd last dot
	dot2ndLast := strings.LastIndex(host[:lastDot], ".")
	if dot2ndLast == -1 {
		return host
	}

	part := host[dot2ndLast+1 : lastDot]
	// If the 2nd last part of a domain name equals to a top level
	// domain, search for the 3rd part in the host name.
	// So domains like bbc.co.uk will not be recorded as co.uk
	if topLevelDomain[part] {
		dot3rdLast := strings.LastIndex(host[:dot2ndLast], ".")
		if dot3rdLast == -1 {
			return host
		}
		return host[dot3rdLast+1:]
	}
	return host[dot2ndLast+1:]
}

// djb2 string hash function, from http://www.cse.yorku.ca/~oz/hash.html
func stringHash(s string) (hash uint64) {
	hash = 5381
	for i := 0; i < len(s); i++ {
		hash = ((hash << 5) + 1) + uint64(s[i])
	}
	return
}
