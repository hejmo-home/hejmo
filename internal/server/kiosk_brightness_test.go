package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/kiosk"
)

// fakeKiosk stands in for FreeKiosk on a tablet; the adapter itself is tested
// against a fake REST API in internal/kiosk. It keeps the device's state, so
// the runner can read it back through Status.
type fakeKiosk struct {
	mu        sync.Mutex
	calls     []string // "brightness 40", "screen_off", ...
	who       []string // "url key" of every call
	fail      bool
	sensor    bool
	screenOff bool
}

func (f *fakeKiosk) factory() kiosk.Factory {
	return func(base, key string) kiosk.Adapter { return fakeAdapter{f, base + " " + key} }
}

type fakeAdapter struct {
	f   *fakeKiosk
	who string
}

func (a fakeAdapter) record(call string) error {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	if a.f.fail {
		return errors.New("tablet unreachable")
	}
	a.f.calls = append(a.f.calls, call)
	a.f.who = append(a.f.who, a.who)
	return nil
}

func (a fakeAdapter) SetBrightness(_ context.Context, p int) error {
	return a.record(fmt.Sprintf("brightness %d", p))
}
func (a fakeAdapter) EnableAutoBrightness(context.Context) error { return a.record("auto") }
func (a fakeAdapter) ScreenOff(context.Context) error {
	err := a.record("screen_off")
	if err == nil {
		a.f.mu.Lock()
		a.f.screenOff = true
		a.f.mu.Unlock()
	}
	return err
}
func (a fakeAdapter) ScreenOn(context.Context) error {
	err := a.record("screen_on")
	if err == nil {
		a.f.mu.Lock()
		a.f.screenOff = false
		a.f.mu.Unlock()
	}
	return err
}
func (a fakeAdapter) Status(context.Context) (kiosk.Status, error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	if a.f.fail {
		return kiosk.Status{}, errors.New("tablet unreachable")
	}
	return kiosk.Status{HasLightSensor: a.f.sensor, ScreenOn: !a.f.screenOff}, nil
}

// take returns the calls since the last take.
func (f *fakeKiosk) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls, f.who = nil, nil
	return c
}

func (f *fakeKiosk) setFail(v bool) { f.mu.Lock(); f.fail = v; f.mu.Unlock() }

const tablet = "http://192.168.1.20:8080"

// kioskBox is a family box in Munich whose display has a kiosk app.
func kioskBox(t *testing.T, displayType string, extra url.Values) (*familyBox, *fakeKiosk) {
	t.Helper()
	fake := &fakeKiosk{}
	f := newFamilyBox(t, displayType, withKiosk(fake))
	if _, err := f.db.SQL().Exec(`UPDATE households SET latitude = 48.15, longitude = 11.46`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"kiosk_url": {tablet}, "kiosk_key": {"k-1"}}
	for k, v := range extra {
		form[k] = v
	}
	if r := f.saveSettings(t, form); r.status != http.StatusSeeOther {
		t.Fatalf("settings = %d\n%s", r.status, r.body)
	}
	return f, fake
}

func (f *familyBox) tick(at time.Time) {
	f.now = at
	f.kioskRun.Tick(context.Background())
}

func expect(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: calls = %q, want %q", what, got, want)
	}
}

// 179, Q34, Q76: Day brightness by day, Evening after sunset at the home
// location, each sent once when it begins (tested against a fake kiosk API).
func TestBrightnessFollowsTheSunOnceAtEachSwitch(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", nil)
	f.tick(f.at(14, 14, 0)) // the sun sets in Munich at about 16:36
	if !reflect.DeepEqual(fake.who, []string{tablet + " k-1"}) {
		t.Fatalf("the tablet was asked as %q, want its address and key", fake.who)
	}
	expect(t, "day", fake.take(), "brightness 100")
	f.tick(f.at(14, 16, 0))
	expect(t, "still day", fake.take())
	f.tick(f.at(14, 16, 50))
	expect(t, "sunset", fake.take(), "brightness 40")
	f.tick(f.at(14, 17, 30))
	expect(t, "evening goes on", fake.take())
	f.tick(f.at(15, 12, 0))
	expect(t, "next day", fake.take(), "brightness 100")
}

func TestBrightnessUsesTheAdjustedValuesAndAppliesAChangeAtTheNextTick(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", url.Values{"bright_day": {"80"}, "bright_evening": {"25"}})
	f.tick(f.at(14, 14, 0))
	expect(t, "day", fake.take(), "brightness 80")
	f.tick(f.at(14, 17, 0))
	expect(t, "evening", fake.take(), "brightness 25")
	f.saveSettings(t, url.Values{"bright_evening": {"30"}})
	f.tick(f.at(14, 17, 1))
	expect(t, "changed", fake.take(), "brightness 30")
}

// Q77: quiet mode off is the front light at 0 % on E-Ink and the screen off on a normal screen.
func TestQuietModeOffSwitchesTheFrontLightOrTheScreenOff(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", quietForm(url.Values{"quiet_from": {"12:00"}, "quiet_to": {"14:00"}}))
	f.tick(f.at(14, 11, 59))
	fake.take()
	f.tick(f.at(14, 12, 0))
	expect(t, "E-Ink quiet", fake.take(), "brightness 0")
	f.tick(f.at(14, 14, 0))
	expect(t, "E-Ink after quiet", fake.take(), "brightness 100")

	f, fake = kioskBox(t, "screen", quietForm(url.Values{"quiet_from": {"12:00"}, "quiet_to": {"14:00"}}))
	f.tick(f.at(14, 11, 59))
	fake.take()
	f.tick(f.at(14, 12, 0))
	expect(t, "screen quiet", fake.take(), "screen_off")
	f.tick(f.at(14, 12, 1))
	expect(t, "screen stays off", fake.take())
	f.tick(f.at(14, 14, 0))
	expect(t, "screen after quiet", fake.take(), "screen_on", "brightness 100")
}

func TestQuietModeCanDimInsteadOfSwitchingOff(t *testing.T) {
	f, fake := kioskBox(t, "screen", quietForm(url.Values{"quiet_from": {"12:00"}, "quiet_to": {"14:00"}, "quiet_light": {"dim"}, "bright_quiet": {"7"}}))
	f.tick(f.at(14, 11, 59))
	fake.take()
	f.tick(f.at(14, 12, 0))
	expect(t, "dimmed", fake.take(), "brightness 7")
}

// Q78: room light is offered only when the device reports a sensor; then its sensor regulates.
func TestRoomLightHandsTheBrightnessToTheDevicesSensorOnlyWhenItHasOne(t *testing.T) {
	f, fake := kioskBox(t, "screen", url.Values{"light_source": {"room"}})
	f.tick(f.at(14, 14, 0))
	expect(t, "room light without a sensor", fake.take(), "brightness 100") // the sun values keep working
	fake.mu.Lock()
	fake.sensor = true
	fake.mu.Unlock()
	f.tick(f.at(14, 14, 1))
	expect(t, "room light with a sensor", fake.take(), "auto")
	f.tick(f.at(14, 17, 0))
	expect(t, "the sun no longer matters", fake.take())
}

// Q79: a touch in quiet mode wakes a switched-off display for a minute at evening brightness.
func TestATouchWakesTheDisplayForAMinuteInQuietMode(t *testing.T) {
	f, fake := kioskBox(t, "screen", quietForm(url.Values{"wake_minutes": {"2"}}))
	f.tick(f.at(14, 23, 0))
	expect(t, "quiet", fake.take(), "screen_off")
	f.now = f.at(14, 23, 5)
	f.kioskRun.Wake(f.displayID(t))
	f.tick(f.at(14, 23, 5))
	expect(t, "woken", fake.take(), "screen_on", "brightness 40")
	f.tick(f.at(14, 23, 6))
	expect(t, "still awake", fake.take())
	f.tick(f.at(14, 23, 7))
	expect(t, "off again", fake.take(), "screen_off")
}

func TestAWakeOutsideQuietModeChangesNothing(t *testing.T) {
	f, fake := kioskBox(t, "screen", nil)
	f.tick(f.at(14, 14, 0))
	fake.take()
	f.kioskRun.Wake(f.displayID(t))
	f.tick(f.at(14, 14, 1))
	expect(t, "daytime wake", fake.take())
}

func TestBrightnessIsRetriedAfterAFailureAndTheStateSaysSo(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", nil)
	id := f.displayID(t)
	fake.setFail(true)
	f.tick(f.at(14, 17, 0))
	st, ok := f.kioskRun.State(id)
	if !ok || st.Reachable || st.Confirmed {
		t.Fatalf("state after a failure = %+v, %v", st, ok)
	}
	if st.Target.Percent != 40 {
		t.Fatalf("the state still says what should have happened: %+v", st.Target)
	}
	fake.setFail(false)
	f.tick(f.at(14, 17, 1))
	expect(t, "retry", fake.take(), "brightness 40")
	st, _ = f.kioskRun.State(id)
	if !st.Reachable || !st.Confirmed || !st.ConfirmedAt.Equal(f.at(14, 17, 1)) {
		t.Fatalf("state after the retry = %+v", st)
	}
}

func TestDisplaysWithoutKioskOrWithoutLocationAreLeftAlone(t *testing.T) {
	fake := &fakeKiosk{}
	f := newFamilyBox(t, "eink_bw", withKiosk(fake))
	f.quietOn(t)
	f.tick(f.at(14, 23, 0))
	expect(t, "no kiosk app", fake.take()) // only the page goes quiet
	if _, ok := f.kioskRun.State(f.displayID(t)); ok {
		t.Fatal("state for a display without a kiosk app")
	}
	// A kiosk app but no home coordinates: Day applies all day, quiet mode still applies.
	if _, err := f.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL`); err != nil {
		t.Fatal(err)
	}
	f.saveSettings(t, url.Values{"kiosk_url": {"http://tablet:8080"}})
	f.tick(f.at(14, 18, 0))
	expect(t, "no location", fake.take(), "brightness 100")
	f.saveSettings(t, quietForm(url.Values{"kiosk_url": {"http://tablet:8080"}}))
	f.tick(f.at(14, 22, 5))
	expect(t, "quiet without location", fake.take(), "brightness 0")
}

func TestRemovingTheKioskAppForgetsItsState(t *testing.T) {
	f, _ := kioskBox(t, "eink_bw", nil)
	f.tick(f.at(14, 14, 0))
	f.saveSettings(t, url.Values{"kiosk_url": {""}})
	f.tick(f.at(14, 14, 1))
	if _, ok := f.kioskRun.State(f.displayID(t)); ok {
		t.Fatal("state kept after the kiosk app was removed")
	}
}
