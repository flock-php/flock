package ports

import (
	"slices"
	"testing"
)

func TestNetstatListeners(t *testing.T) {
	out := `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)          rxbytes      txbytes  rhiwat  shiwat          process:pid    state  options           gencnt    flags   flags1 usecnt rtncnt fltrs
tcp4       0      0  127.0.0.1.80           *.*                    LISTEN                 0            0  131072  131072      nginx-arm64:36959  00180 00000006 00000000000e1cca 00000000 00000800      1      0 000000
tcp4       0      0  127.0.0.1.53           *.*                    LISTEN                 0            0  131072  131072    dnsmasq-arm64:36940  00180 00000006 00000000000e1cc5 00000000 00000800      1      0 000000
tcp4       0      0  127.0.0.1.41343        *.*                    LISTEN                 0            0  131072  131072        LM Studio:880    00100 00000006 0000000000073a3f 00000000 00080800      1      0 000000
tcp46      0      0  *.8080                 *.*                    LISTEN                 0            0  131072  131072         rapportd:678    00100 00000006 00000000000ca567 00000000 00080800      1      0 000000
tcp4       0      0  192.168.0.41.59741     160.79.104.10.80       ESTABLISHED         7386        49307  131072  132352          2.1.302:49688  00102 00000008 00000000000e6b6f 00000081 04000900      2      0 000000
tcp4       0      0  127.0.0.1.8080         *.*                    LISTEN      131072  131072    412      0  00100 00000006 0000000000073a3f 00000000 00080800
tcp4       0      0  127.0.0.1.180          *.*                    LISTEN                 0            0  131072  131072      nginx-arm64:1  00180 00000006 00000000000e1cca 00000000 00000800      1      0 000000
`
	for port, want := range map[int][]listener{
		80:    {{PID: 36959, Name: "nginx-arm64"}},
		53:    {{PID: 36940, Name: "dnsmasq-arm64"}},
		41343: {{PID: 880, Name: "LM Studio"}},
		8080:  {{PID: 678, Name: "rapportd"}, {PID: 412}},
		443:   nil,
	} {
		if got := netstatListeners(out, port); !slices.Equal(got, want) {
			t.Errorf("port %d: got %+v, want %+v", port, got, want)
		}
	}
}
