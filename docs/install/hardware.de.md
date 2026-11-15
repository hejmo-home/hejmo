# Hardware

English: [Hardware](hardware.md)

Hejmo läuft auf einem Raspberry Pi 4 oder neuer mit 1 GB Arbeitsspeicher und auf jedem Linux-Rechner, auf dem Docker läuft.

**Raspberry Pi: in der Emulation getestet.** Bei jedem Build startet die Box auf einem arm64-Rechner in einem Container, der wie ein Raspberry Pi begrenzt ist (1 CPU, 1 GB Speicher), mit einem Beispielhaushalt aus vier Personen und acht synchronisierten Kalendern mit je 2.000 Terminen. Der Build schlägt fehl, wenn ein Messwert über seiner Obergrenze liegt. Auf einem echten Raspberry Pi hat diese Werte noch niemand bestätigt.

| Messwert | Obergrenze im Build | Build-Rechner (arm64, 1 CPU, 1 GB) | Entwicklungslauf (Mac, Docker Desktop) |
|---|---|---|---|
| Speicher im Leerlauf | 80 MiB | 27,8 MiB | 26 bis 28 MiB |
| Speicher-Spitze, wenn alle Kalender gleichzeitig synchronisieren | 200 MiB | 53,4 MiB | 71 bis 89 MiB |
| Start, bis die Weboberfläche antwortet | 5 s | 0,3 s | 0,4 bis 0,6 s |
| CPU im Leerlauf | 5 % | 0,0 % | 0 % |

Die Spalte Build-Rechner ist das Ergebnis eines Builds; die Werte jedes Builds stehen in seiner CI-Zusammenfassung (Job "Resources (arm64)"). Die Entwicklungsspalte ist der Bereich aus drei Läufen auf einem Mac. Die Obergrenzen stehen in `ci/resource-thresholds.json` und werden nach einigen weiteren Builds enger gesetzt; das Messwerkzeug ist `cmd/hejmo-measure`.

Ein echter Raspberry Pi startet langsamer als der Build-Rechner, vor allem wegen der SD-Karte. Nimm eine gute A1- oder A2-Karte oder eine SSD.
