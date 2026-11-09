package status

import (
	"testing"

	"github.com/Plasmoid77/moneroid/internal/jsonx"
)

func TestPeers(t *testing.T) {
	o, err := jsonx.Decode([]byte(`{"peers":["I,30,-1,,15253322,[2001:db8::1]:37888","O,2398,38,GoObserver v5.0.7,15253322,89.233.207.111:37888","X,1,1,a,1,b","O,x,1,a,1,b","O,1,1,1,2"]}`))
	if err != nil {
		t.Fatal(err)
	}
	ps := peers(o)
	if len(ps) != 2 {
		t.Fatalf("want the 2 well-formed peers, got %+v", ps)
	}
	if !ps[0].Incoming || ps[0].PingMs != nil || ps[0].Software != "" || ps[0].Addr != "[2001:db8::1]:37888" || ps[0].UptimeS != 30 {
		t.Fatalf("incoming peer without a ping: %+v", ps[0])
	}
	if ps[1].Incoming || *ps[1].PingMs != 38 || ps[1].Software != "GoObserver v5.0.7" {
		t.Fatalf("outgoing peer: %+v", ps[1])
	}
}
