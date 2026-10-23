package ports

import (
	"reflect"
	"testing"
)

func TestParseListeners(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 3600007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000   989        0 114249 1 0000000000000000 100 0 0 10 5
   1: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2001 1 0000000000000000 100 0 0 10 5
   2: 0100007F:0050 0100007F:9C40 01 00000000:00000000 00:00000000 00000000  1000        0 2002 1 0000000000000000 100 0 0 10 5
   3: 0100007F:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2003 1 0000000000000000 100 0 0 10 5
`
	// Port 80: the listener, not the established connection to it.
	if got := parseListeners(table, 80); !reflect.DeepEqual(got, map[string]int{"2001": 0}) {
		t.Errorf("80: %v", got)
	}
	if got := parseListeners(table, 443); !reflect.DeepEqual(got, map[string]int{"2003": 1000}) {
		t.Errorf("443: %v", got)
	}
	if got := parseListeners(table, 8080); len(got) != 0 {
		t.Errorf("8080: %v", got)
	}
}

func TestSystemdHint(t *testing.T) {
	h := Holder{Port: 80, Process: "apache2", Systemd: "apache2"}
	if got := h.Hint(); got != "run `sudo systemctl disable --now apache2`" {
		t.Errorf("Hint() = %q", got)
	}
}
