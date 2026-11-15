package publiccert_test

import (
	"errors"
	"net"
	"testing"
)

// The UDP port is picked by the kernel; the TCP port of the same number may
// already be taken by another process. The pair is retried with a new port.
func TestListenPairRetriesWhenTheTCPPortIsTaken(t *testing.T) {
	calls := 0
	listen := func(network, addr string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("bind: address already in use")
		}
		return net.Listen(network, addr)
	}
	pc, ln, err := listenPair(listen)
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	defer ln.Close()
	if calls != 2 {
		t.Errorf("tcp listen calls = %d, want 2", calls)
	}
	if pc.LocalAddr().String() != ln.Addr().String() {
		t.Errorf("udp %v and tcp %v differ", pc.LocalAddr(), ln.Addr())
	}
}
