package policy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/common"
	"google.golang.org/protobuf/encoding/protowire"
)

var geoIPCache = struct {
	sync.RWMutex
	entries map[string][]netip.Prefix
}{
	entries: make(map[string][]netip.Prefix),
}

var defaultGeoIPPath = struct {
	sync.RWMutex
	value string
}{}

func ResetGeoIPCache() {
	geoIPCache.Lock()
	geoIPCache.entries = make(map[string][]netip.Prefix)
	geoIPCache.Unlock()
}

func SetDefaultGeoIPPath(path string) {
	defaultGeoIPPath.Lock()
	defaultGeoIPPath.value = strings.TrimSpace(path)
	defaultGeoIPPath.Unlock()
}

func ResolveGeoIPPath(configPath, configuredPath string) string {
	if path := strings.TrimSpace(configuredPath); path != "" {
		if filepath.IsAbs(path) {
			return path
		}
		if configPath != "" {
			return filepath.Join(filepath.Dir(configPath), path)
		}
		return common.ResolvePath(path)
	}
	if configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "geoip.dat")
	}
	return filepath.Join(common.GetRunPath(), "conf", "geoip.dat")
}

func resolveGeoIPPath(opts Options) string {
	if path := strings.TrimSpace(opts.GeoIPPath); path != "" {
		return ResolveGeoIPPath("", path)
	}
	defaultGeoIPPath.RLock()
	path := defaultGeoIPPath.value
	defaultGeoIPPath.RUnlock()
	if path != "" {
		return path
	}
	return filepath.Join(common.GetRunPath(), "conf", "geoip.dat")
}

func loadGeoIPPrefixes(opts Options, path, code string) ([]netip.Prefix, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, fmt.Errorf("empty geoip code")
	}
	if prefixes, ok := builtinGeoIPPrefixes(code); ok {
		return prefixes, nil
	}
	if path == "" {
		path = resolveGeoIPPath(opts)
	}
	cacheKey := path + "\x00" + code

	geoIPCache.RLock()
	if prefixes, ok := geoIPCache.entries[cacheKey]; ok {
		geoIPCache.RUnlock()
		return append([]netip.Prefix(nil), prefixes...), nil
	}
	geoIPCache.RUnlock()

	prefixes, err := readGeoIPPrefixes(path, code)
	if err != nil {
		return nil, err
	}

	geoIPCache.Lock()
	geoIPCache.entries[cacheKey] = append([]netip.Prefix(nil), prefixes...)
	geoIPCache.Unlock()
	return prefixes, nil
}

func readGeoIPPrefixes(path, code string) ([]netip.Prefix, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	record, err := findFramedCodeRecord(file, []byte(code))
	if err != nil {
		return nil, err
	}
	prefixes, err := decodeGeoIPRecord(record)
	if err != nil {
		return nil, err
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("geoip code %s has no prefixes", code)
	}
	return prefixes, nil
}

func findFramedCodeRecord(r io.Reader, code []byte) ([]byte, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("empty code")
	}
	br := bufio.NewReaderSize(r, 64*1024)
	need := 2 + len(code)
	prefixBuf := make([]byte, need)

	for {
		if _, err := br.ReadByte(); err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("code %s not found", string(code))
			}
			return nil, err
		}
		size, err := decodeVarint(br)
		if err != nil {
			return nil, err
		}
		bodyLen := int(size)
		if bodyLen <= 0 {
			return nil, fmt.Errorf("invalid geoip record length %d", bodyLen)
		}

		prefixLen := bodyLen
		if prefixLen > need {
			prefixLen = need
		}
		prefix := prefixBuf[:prefixLen]
		if _, err := io.ReadFull(br, prefix); err != nil {
			return nil, err
		}

		match := bodyLen >= need && int(prefix[1]) == len(code) && bytes.Equal(prefix[2:need], code)
		remain := bodyLen - prefixLen
		if match {
			record := make([]byte, bodyLen)
			copy(record, prefix)
			if remain > 0 {
				if _, err := io.ReadFull(br, record[prefixLen:]); err != nil {
					return nil, err
				}
			}
			return record, nil
		}
		if remain > 0 {
			if _, err := br.Discard(remain); err != nil {
				return nil, err
			}
		}
	}
}

func decodeVarint(r *bufio.Reader) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= (uint64(b) & 0x7f) << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("varint overflow")
}

func decodeGeoIPRecord(record []byte) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, 64)
	for len(record) > 0 {
		num, typ, n := protowire.ConsumeTag(record)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		record = record[n:]
		switch num {
		case 2:
			if typ != protowire.BytesType {
				skip, err := skipFieldValue(num, typ, record)
				if err != nil {
					return nil, err
				}
				record = record[skip:]
				continue
			}
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return nil, protowire.ParseError(m)
			}
			record = record[m:]
			prefix, ok, err := decodeCIDRRecord(value)
			if err != nil {
				return nil, err
			}
			if ok {
				prefixes = append(prefixes, prefix)
			}
		default:
			skip, err := skipFieldValue(num, typ, record)
			if err != nil {
				return nil, err
			}
			record = record[skip:]
		}
	}
	return prefixes, nil
}

func decodeCIDRRecord(record []byte) (netip.Prefix, bool, error) {
	var (
		ipBytes []byte
		bits    int
	)
	for len(record) > 0 {
		num, typ, n := protowire.ConsumeTag(record)
		if n < 0 {
			return netip.Prefix{}, false, protowire.ParseError(n)
		}
		record = record[n:]
		switch num {
		case 1:
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return netip.Prefix{}, false, protowire.ParseError(m)
			}
			record = record[m:]
			ipBytes = append(ipBytes[:0], value...)
		case 2:
			value, m := protowire.ConsumeVarint(record)
			if m < 0 {
				return netip.Prefix{}, false, protowire.ParseError(m)
			}
			record = record[m:]
			bits = int(value)
		default:
			skip, err := skipFieldValue(num, typ, record)
			if err != nil {
				return netip.Prefix{}, false, err
			}
			record = record[skip:]
		}
	}
	addr, ok := netip.AddrFromSlice(ipBytes)
	if !ok {
		return netip.Prefix{}, false, nil
	}
	prefix := netip.PrefixFrom(addr.Unmap(), bits)
	if !prefix.IsValid() {
		return netip.Prefix{}, false, nil
	}
	return prefix.Masked(), true, nil
}

func skipFieldValue(num protowire.Number, typ protowire.Type, record []byte) (int, error) {
	skip := protowire.ConsumeFieldValue(num, typ, record)
	if skip < 0 {
		return 0, protowire.ParseError(skip)
	}
	return skip, nil
}

func builtinGeoIPPrefixes(code string) ([]netip.Prefix, bool) {
	if code != "PRIVATE" {
		return nil, false
	}
	values := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"169.254.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err == nil {
			prefixes = append(prefixes, prefix.Masked())
		}
	}
	return prefixes, true
}
