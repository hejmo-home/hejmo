# Reference display: which E-Ink tablet to buy

Researched 14/11/2026 for issue #42 (map #40). Builds on ADR 0005. Sources are listed in section 6; claims carry their source number in brackets.

**Status markers**
- **verified**: read in source code, official docs or a hardware-tested pull request
- **reported**: from a vendor or press page, not checked on hardware
- **unverified**: inference; must be tested on the device before package 5

---

## 1. Recommendation

| Role | Device | Panel | Android | Price (DE) | Front-light route |
|---|---|---|---|---|---|
| **Colour** | **Onyx Boox Note Air5 C** | 10.3" Kaleido 3 | 15 | about €530 | Onyx `DeviceController`, hardware-tested in KOReader [4][5] |
| **Black and white** | **Onyx Boox Go 10.3 (Gen II) Lumi** | 10.3" Carta 1200, 300 ppi | 15 | about €450 | Onyx `DeviceController`, hardware-tested in KOReader [4][6] |

Buy the **Lumi** variant of the Go 10.3 Gen II. The plain Go 10.3 Gen II (€420) has **no front light** [8].

Why these two: they are the only ~10" Android E-Ink tablets on sale in Germany where an open-source app has been shown, on the device, to switch and dim the front light without root. Both run the same Boox software on Android 15, so one adapter serves both.

**Fallback (colour):** Onyx Boox Note Air6 C (Android 16, €580 RRP, announced 16/09/2026) [10]. Same vendor and probably the same light API, but **unverified**: KOReader has no entry for it yet. Only buy it if the Air5 C is sold out.

**Not recommended:** Bigme (B10, B1051C). No open-source front-light route found, and the one FreeKiosk bug report on a Bigme device (B6, Android 14) describes several failures [3].

---

## 2. Key finding: FreeKiosk alone probably does not drive the Onyx front light

- FreeKiosk sets brightness only through standard Android paths: the window attribute `screenBrightness` and `Settings.System.SCREEN_BRIGHTNESS` (via `WRITE_SETTINGS` or Device Owner) **(verified)** [2].
- FreeKiosk's source and issues contain no Onyx- or Boox-specific code; the only E-Ink report is the Bigme B6 bug **(verified)** [2][3].
- Onyx front lights are driven by the vendor class `android.onyx.hardware.DeviceController` (`setLightValue(type, value)`). KOReader calls it by reflection from an ordinary app, no root, no extra permission (`needsPermission() = false`) **(verified)** [4].
- KOReader's Note Air5 C test log shows the generic driver was in use before the Onyx driver was added [5]. This suggests, but does not prove, that standard Android brightness does not reach the front light. Whether FreeKiosk's `POST /api/brightness` changes the light on these devices is **unverified**.

**Consequence for the kiosk adapter:** the open-source route is known and small (one reflection call). Options, in order of preference:
1. **Contribute an Onyx light controller to FreeKiosk** (MIT) that calls `DeviceController.setLightValue` when the device is Onyx. KOReader's launcher code is MIT too [4], so it can be ported with attribution.
2. Use `POST /api/screen/off` at night (see section 3) and accept "off" without dimming, as ADR 0005 already allows.
3. ADR 0005's V2 option: an own wrapper app using the same call.

---

## 3. FreeKiosk facts relevant to the adapter

All **verified** from the REST API docs and repo (commit `b7f4987`, 09/11/2026) [1][2].

- License MIT; stable v2.0.0, current pre-release 2.1.0-beta.2; Google Play or APK from GitHub Releases, not F-Droid; Android 8.0+.
- `GET/POST /api/brightness`, body `{ "value": 0-100 }`; disables auto-brightness; ignored if "App Brightness Control" is off in settings.
- `GET|POST /api/screen/off` and `/api/screen/on`. Screen-off uses, in order: Device Owner `lockNow()`, Device Admin `lockNow()`, AccessibilityService lock (Android 9+), else brightness 0 with screen still on.
- HTTP server stays reachable while the screen is off (v1.2.4+). Default port 8080; optional API key in `X-Api-Key`.
- Device Owner: `adb shell dpm set-device-owner com.freekiosk/.DeviceAdminReceiver`.

**Unverified on Onyx:**
- Whether a real screen-off (`lockNow`) also switches off the front light. Likely, because sleep normally does, but not tested.
- Whether Wi-Fi stays up while the Onyx is asleep, so `/api/screen/on` can wake it. Boox has a setting to turn Wi-Fi off on sleep [7]; it must be off for Hejmo.
- Whether Device Owner provisioning works on Boox firmware.

---

## 4. Device details

### Onyx Boox Note Air5 C (colour)
- 10.3" Kaleido 3; 2480×1860 (300 ppi) B&W, 1240×930 (150 ppi) colour; two-tone front light; Android 15; 6 GB RAM, 64 GB + microSD **(reported)** [9][11].
- Price: €529.99 at the Boox EU shop at launch (24/10/2025) [11]; German retailers around €529–549 **(reported)**. Later US price $499.99 suggests discounts are possible.
- Front light: KOReader PR #577, merged 09/01/2026, tested on a Note Air5 C (`model noteair5c`) with the Onyx controller **(verified)** [5].
- Risk: superseded by the Air6 C; stock may run out.

### Onyx Boox Go 10.3 (Gen II) Lumi (black and white)
- 10.3" E Ink, 300 ppi; two-tone front light with 32 steps each for warm and cold; Android 15; 4 GB RAM, 64 GB, no microSD **(reported)** [7][8].
- Price: €449.99 (Lumi) and €419.99 (no light) at launch, 17/03/2026 [8] **(reported)**.
- Front light: KOReader PR #613, merged 29/08/2026, tested on hardware: `checkCTM() = true`, max 32/32, `setLightValue()` works **(verified)** [6].
- No BSR acceleration, so refreshes are slower than the Air5 C [7]. Fine for a calm wall display.

### Onyx Boox Note Air6 C (colour fallback)
- 10.3" Kaleido 3, front light with adjustable colour temperature, Android 16, €580 RRP, sold through Boox's site and Amazon **(reported)** [10].
- No KOReader entry, no FreeKiosk report: front-light route **unverified**.

### Bigme B10 (not recommended)
- 10.3" Kaleido 3, Android 14, 4G, 8 GB/256 GB; about €599 in the Bigme EU shop, reported out of stock **(reported)** [12].
- No open-source front-light route found; KOReader has no Bigme light driver **(verified)** [4].

### Ruled out
- **Onyx Boox Go 10.3 Gen I** and **Go 10.3 Gen II without Lumi**: no front light.
- **Onyx Boox Tab X C**: 13.3", too large, about €700+.
- **Onyx Boox Tab Ultra C Pro**: Android 12 and older; Note Air5 C is newer and cheaper.

---

## 5. Before buying / first test on the device

1. Install FreeKiosk, call `POST /api/brightness` with 0 and 100: does the front light change?
2. Call `POST /api/screen/off` with Device Owner set: is the light off, and does `/api/screen/on` wake it over Wi-Fi?
3. If step 1 fails: confirm the KOReader route by installing KOReader and moving its light slider. This proves the `DeviceController` path for the FreeKiosk contribution.

---

## 6. Sources

1. FreeKiosk REST API docs: https://raw.githubusercontent.com/RushB-fr/freekiosk/main/docs/rest-api.md
2. FreeKiosk repository (README, `BrightnessPrefs.kt`, `KioskModule.kt`, `ScreenController.kt`, `CHANGELOG.md`), commit `b7f4987`: https://github.com/RushB-fr/freekiosk
3. FreeKiosk issue #275, Bigme B6: https://github.com/RushB-fr/freekiosk/issues/275
4. KOReader Android launcher (MIT), `LightsFactory.kt`, `OnyxAdbLightsController.kt`, `DeviceInfo.kt`, commit `eaed834`: https://github.com/koreader/android-luajit-launcher
5. KOReader launcher PR #577, Note Air5 C: https://github.com/koreader/android-luajit-launcher/pull/577
6. KOReader launcher PR #613, Go 10.3 Gen II Lumi: https://github.com/koreader/android-luajit-launcher/pull/613
7. Parka Blogs review, Go 10.3 Gen II Lumi: https://www.parkablogs.com/content/boox-go-10-3-gen-2-lumi-e-ink-tablet-review
8. ifun.de, Go 10.3 Gen II launch and EUR prices (17/03/2026): https://www.ifun.de/boox-stellt-neue-e-paper-tablets-mit-103-zoll-display-vor-276397/ ; The eBook Reader blog: https://blog.the-ebook-reader.com/2026/03/17/new-boox-go-10-3-gen-2-released-with-frontlight-and-android-15/
9. Notebookcheck, Note Air5 C launch: https://www.notebookcheck.net/Boox-Note-Air5-C-launches-with-color-display-and-physical-keyboard-support-for-529-99.1143277.0.html
10. Notebookcheck, Note Air6 C (16/09/2026): https://www.notebookcheck.com/Neues-E-Ink-Tablet-startet-mit-Android-16-Farbdisplay-und-schnellerem-Prozessor.1400916.0.html
11. appgefahren.de, Note Air5 C EUR price (24/10/2025): https://www.appgefahren.de/?p=388705
12. Bigme B10: Android Authority https://www.androidauthority.com/bigme-b10-tablet-launch-3637013/ ; Bigme EU shop https://euroshop.bigme.vip/products/bigme-b10-10-34g-premium-color-e-paper-digital-tablet-with-android-14os

Prices are launch or list prices; check Galaxus, Amazon.de or the Boox EU shop on the day of purchase.
