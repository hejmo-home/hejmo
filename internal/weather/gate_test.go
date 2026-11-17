package weather_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/weather"
)

type switchPolicy struct{ off atomic.Bool }

func (p *switchPolicy) Verdict(outbound.Service) outbound.Verdict {
	if p.off.Load() {
		return outbound.VerdictOffline
	}
	return outbound.VerdictAllowed
}

type countLedger struct{ refused, contacts atomic.Int32 }

func (l *countLedger) Refused(time.Time, outbound.Service, string)   { l.refused.Add(1) }
func (l *countLedger) Contacted(time.Time, outbound.Service, string) { l.contacts.Add(1) }

func TestWeatherIsAskedThroughTheGateAndRefusedWhileOffline(t *testing.T) {
	srv, hits, _ := openMeteo(t, sunny, 200)
	pol, led := &switchPolicy{}, &countLedger{}
	s := weather.New(weather.WithBaseURL(srv.URL), weather.WithGate(outbound.New(pol, led)))

	if err := s.Refresh(context.Background(), 48.1, 11.4); err != nil {
		t.Fatal(err)
	}
	if led.contacts.Load() != 1 {
		t.Errorf("contacts = %d, want the weather contact recorded", led.contacts.Load())
	}
	pol.off.Store(true)
	err := s.Refresh(context.Background(), 48.1, 11.4)
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Refresh = %v, want refused", err)
	}
	if hits.Load() != 1 || led.refused.Load() != 1 {
		t.Errorf("hits = %d, refused = %d, want 1 and 1", hits.Load(), led.refused.Load())
	}
	// The last reading is kept, as after any failure.
	if _, ok := s.Latest(48.1, 11.4); !ok {
		t.Error("the last reading is gone")
	}
}

func TestTheWeatherLoopTriesNothingWhileItIsOff(t *testing.T) {
	srv, hits, _ := openMeteo(t, sunny, 200)
	pol, led := &switchPolicy{}, &countLedger{}
	pol.off.Store(true)
	s := weather.New(weather.WithBaseURL(srv.URL), weather.WithGate(outbound.New(pol, led)), weather.WithRetry(5*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	s.Run(ctx, 10*time.Millisecond, home{48.1, 11.4}, func(error) {})
	if hits.Load() != 0 || led.refused.Load() != 0 {
		t.Errorf("hits = %d, refused = %d: a connection that is off is not even tried", hits.Load(), led.refused.Load())
	}
}

type home struct{ lat, lon float64 }

func (h home) Home() (float64, float64, error) { return h.lat, h.lon, nil }
