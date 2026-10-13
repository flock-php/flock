package cli

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

// guiSubsystem returns a copy of a Windows console executable marked as a GUI program, the only
// difference `-ldflags -H=windowsgui` makes. Windows then starts it without a console window,
// which is what the background daemon (flockd.exe) needs; the CLI itself must stay a console
// program so shells wait for it and see its output.
func guiSubsystem(exe []byte) ([]byte, error) {
	if len(exe) < 0x40 || exe[0] != 'M' || exe[1] != 'Z' {
		return nil, errors.New("not a Windows executable")
	}
	peOffset := int(binary.LittleEndian.Uint32(exe[0x3c:]))
	// PE signature (4 bytes) + COFF file header (20 bytes), then the optional header, whose
	// Subsystem field sits at offset 68 in both the PE32 and PE32+ layouts.
	field := peOffset + 4 + 20 + 68
	if field+2 > len(exe) || !bytes.Equal(exe[peOffset:peOffset+4], []byte("PE\x00\x00")) {
		return nil, errors.New("malformed PE header")
	}

	out := bytes.Clone(exe)
	switch sub := binary.LittleEndian.Uint16(out[field:]); sub {
	case pe.IMAGE_SUBSYSTEM_WINDOWS_CUI:
		binary.LittleEndian.PutUint16(out[field:], pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
	case pe.IMAGE_SUBSYSTEM_WINDOWS_GUI:
	default:
		return nil, fmt.Errorf("unexpected PE subsystem %d", sub)
	}

	// Double-check with the standard library's parser.
	f, err := pe.NewFile(bytes.NewReader(out))
	if err != nil {
		return nil, err
	}
	var sub uint16
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		sub = h.Subsystem
	case *pe.OptionalHeader32:
		sub = h.Subsystem
	}
	if sub != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		return nil, errors.New("could not mark the executable as a GUI program")
	}
	return out, nil
}
