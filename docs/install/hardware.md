# Hardware

Hejmo runs on a Raspberry Pi 4 or newer with 1 GB of memory, and on any Linux machine that runs Docker.

**Raspberry Pi: tested in emulation.** Every build starts the box in a container limited like a Raspberry Pi (1 CPU, 1 GB memory) on an arm64 machine, with a seeded example household of four people and eight synced calendars of 2,000 events each. The build fails when a measurement is above its ceiling. Nobody has confirmed these values on a real Raspberry Pi yet.

| Measurement | Ceiling the build enforces | Development run (Docker Desktop on arm64, 1 CPU, 1 GB) |
|---|---|---|
| Idle memory | 80 MiB | 26 to 28 MiB |
| Peak memory while all calendars sync at once | 200 MiB | 71 to 89 MiB |
| Start until the web interface answers | 5 s | 0.4 to 0.6 s |
| CPU while idle | 5 % | 0 % |

The measured values are the range of three development runs on a Mac, not from the build machine, until the first CI numbers replace them; each build's own values are in its CI job summary (job "Resources (arm64)"). The ceilings live in `ci/resource-thresholds.json`; the measuring tool is `cmd/hejmo-measure`.

A real Raspberry Pi starts slower than the build machine, mostly because of its SD card. Use a good A1 or A2 card or an SSD.
