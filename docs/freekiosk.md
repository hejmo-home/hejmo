# Setting up FreeKiosk for Hejmo

A web page cannot change a screen's brightness or switch it off. For that a display needs a kiosk app. Hejmo works with [FreeKiosk](https://freekiosk.app) (MIT licence, open source, Android). With it the box sets the brightness by time of day and in quiet mode: **Day** from sunrise to sunset, **Evening** from sunset to sunrise, and in quiet mode off or strongly dimmed (settings of a display, block "Brightness").

Without a kiosk app everything else works: a normal screen shows a black page in quiet mode, an E-Ink display its night page. Only the brightness stays as it is.

## What you need

- An Android tablet that shows the display page of your box (the display you already paired).
- FreeKiosk on that tablet, with the box's display address as its start page.
- The tablet and the box in the same network.

## Steps

1. Install FreeKiosk on the tablet (see the project's [installation guide](https://github.com/RushB-fr/freekiosk/blob/main/docs/installation.md)) and set the display address of your box as its page.
2. In FreeKiosk open the settings (five taps on the secret button, then the PIN), go to **Advanced** and switch on **Enable REST API**. Note the port (default 8080) and set an API key.
3. In Hejmo open **Displays**, the display, and under **Brightness** the section **Kiosk app**. Enter the address, for example `http://192.168.1.20:8080` (the tablet's address and the port), and the API key. Save.
4. After a minute the block shows "Now 100 %" or similar and "The kiosk app confirmed it". If it shows "The kiosk app does not answer", check that the tablet is on and in the Wi-Fi; the box tries again every minute.

## What the box sends

The box talks to the tablet over your home network only, with the API key in the `X-Api-Key` header (the key is stored sealed on the box and never shown again):

| When | Request |
|---|---|
| Brightness changes (Day, Evening, dimmed, front light off) | `POST /api/brightness` with `{"value": 40}` |
| The device's light sensor should regulate ("Room light") | `POST /api/autoBrightness/enable` |
| Quiet mode "Off" on a normal screen, and the wake after a touch | `POST /api/screen/off`, `POST /api/screen/on` |
| Every minute: does it answer, does the device report a light sensor | `GET /api/status` |

On E-Ink the brightness is the front light, so "Off" sets it to 0 %. E-Ink front light control is **experimental**: it has not been tested on a real display yet.

## Good to know

- **Light sensor:** the choice between "Sunlight" and "Room light" appears only when the device reports a light sensor in `GET /api/status`.
- **Screen off:** FreeKiosk switches a screen off properly only with device owner, device admin or the accessibility service; without them it dims to 0 %. See its documentation.
- **A touch in quiet mode** wakes a screen that quiet mode switched off for the minutes you set (default 1), at Evening brightness with the night page, and then off again. Brightness only changes through the box: if you change it on the tablet, the box sets it again at the next switch.
