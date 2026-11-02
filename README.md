# GarminDaily

Minimalny program Go: Garmin Connect Personal API → PostgreSQL, jedno konto
Garmin i jeden rekord na datę w każdej z trzech tabel. Uruchamia synchronizację
i kończy pracę. Każde konto wymaga osobnej bazy lub schematu PostgreSQL.

## Uruchomienie

Wymagania: Go zgodne z `go.mod`, PostgreSQL oraz klucz **Personal API** Garmina.
Docker Compose udostępnia izolowaną bazę do prób; nie jest konfiguracją produkcyjną.

```sh
go build -o garmin-daily ./cmd/garmin-daily
docker compose --env-file /dev/null up -d --wait db

# .env jest lokalny, nie jest automatycznie odczytywany przez program.
# Jeśli już istnieje, uzupełnij go zamiast nadpisywać.
# Wzór: .env.example. Nigdy nie zapisuj prawdziwego klucza w Git.
set -a
. ./.env
set +a
export DATABASE_URL='postgres://garmin_daily:dev-only-password@localhost:15432/garmin_daily?sslmode=disable'

./garmin-daily migrate
./garmin-daily sync --from 2026-10-25 --to 2026-10-31
./garmin-daily sync --last-days 7 --timezone UTC
```

CLI, synchronizacja i PostgreSQL znajdują się w `cmd/garmin-daily/`.
Pakiet `garmin/` zawiera niezależnego klienta Personal API i modele odpowiedzi;
korzysta wyłącznie z biblioteki standardowej Go. Endpointy są zebrane w jego
`client.go`, a wartości dziesiętne zachowuje jako `json.Number`. Konwersja do
PostgreSQL pozostaje w aplikacji.

Klienta tworzy się przez `garmin.NewClient(baseURL, httpClient)`, uwierzytelnia
przez `Authenticate(ctx, key)`, a dane pobiera przez `DailySummary`, `DailySleep`
i `DailyHRV`. Dla snu i HRV odpowiedź HTTP 204 zwraca `nil, nil`.
Wywołania przyjmują datę `YYYY-MM-DD`; klient nie odczytuje środowiska ani plików.
Przekazany `http.Client` określa timeout i politykę przekierowań. Klient przechowuje
jeden token w pamięci i jest używany sekwencyjnie.

`migrate` uruchamia początkową migrację `cmd/garmin-daily/schema.sql` w jednej transakcji, **raz**
na wybranej bazie/schemacie. Ponowne uruchomienie zgłasza błąd istniejących tabel.
Nie ma automatycznego migrowania przy synchronizacji ani frameworka migracji.
Kolejna zmiana schematu wymaga jawnej migracji.

`GARMIN_API_KEY` jest wymagany wyłącznie dla `sync`; `DATABASE_URL` dla obu poleceń.
Nie przekazuj sekretów jako argumentów CLI. Dla zdalnej bazy skonfiguruj TLS
w `DATABASE_URL` zgodnie z konfiguracją serwera; `sslmode=disable` powyżej dotyczy
wyłącznie lokalnej bazy testowej.

Zakres dat wybierasz przez `--from YYYY-MM-DD --to YYYY-MM-DD` (obie granice
włącznie) albo `--last-days N --timezone UTC`. Tryb względny pobiera
dzisiejszą datę i poprzednie N−1 dat kalendarzowych w podanej strefie; N musi być
dodatnie. Strefa IANA (np. `Europe/Warsaw`) lub `UTC` jest obowiązkowa w tym trybie; `Local` nie jest
akceptowane. Baza stref czasowych jest wbudowana w binarkę. Użyj strefy
odpowiadającej datom konta Garmin. Trybów nie można łączyć, a `--timezone` bez
`--last-days` jest błędem. Daty bezwzględne pozostają datami kalendarzowymi Garmina,
bez przeliczania na dzień serwera. Powtórzenie zakresu pobiera korekty; nie ma
domyślnego zakresu. Uruchamiaj jeden proces zapisujący do tych tabel naraz.

Sukces: kod wyjścia 0 i `day=YYYY-MM-DD changed_rows=N` dla każdego zapisanego dnia.
Błąd: kod 1, opis etapu i zakończenie zakresu na pierwszym błędzie. Dni wcześniej
zatwierdzone pozostają zapisane; wznowienie polega na powtórzeniu zakresu.

## Kontrakt danych i mały schemat

`daily_health`, `daily_sleep` i `daily_hrv` mają klucz `day DATE`.
Sen i HRV wskazują kluczem obcym na dzień zdrowia. Nie ma identyfikatorów
technicznych, kont/użytkowników, metadanych urządzenia, dodatkowych indeksów,
surowych odpowiedzi, JSONB, próbek intraday ani tabel agregatów 7/30/90 dni.
Źródłowe średnie Garmina, w tym tygodniowa HRV, są oddzielnymi metrykami API;
program nie wylicza i nie przechowuje ich kopii z własnych agregacji.

Typy są celowo małe, ale bez obcinania wartości:

- `smallint` (2 bajty): dzienne tętno, minuty intensywności, stres, Body Battery,
  ocena snu. Wyniki 0–100 mają również ograniczenia `CHECK`.
- `integer` (4 bajty): kroki, kalorie, sekundy snu i HRV. Zwykły dłuższy sen
  przekracza 32767 sekund, a API deklaruje HRV jako int32 bez mniejszego limitu.
- `numeric`: tylko rzeczywiste pola ułamkowe. Dekodowane bez przejścia przez
  `float64`, zachowują dokładność dziesiętną i notację wykładniczą JSON.
- `timestamptz`: początek i koniec snu z epoki UTC w milisekundach. Data snu
  pozostaje datą Garmina; sen przez północ i zmianę czasu nie jest dzielony.
- `text`: status HRV; bez osobnego słownika dla kilku krótkich wartości.

Nie wprowadzamy sztucznych limitów precyzji `numeric(p,s)` ani węższych typów
kosztem możliwego przepełnienia. Przekroczenie obsługiwanego zakresu jest błędem,
nie przycięciem liczby ani zamianą na zero. Rozmiar należy mierzyć na realnym
zbiorze: narzut stron PostgreSQL dominuje przy kilku rekordach.

Mapowanie zachowanych pól (pola w grupie odpowiadają sobie kolejno):

| Tabela / kolumny | Pole Personal API | Jednostka |
| --- | --- | --- |
| każda: `day` | `calendarDate` | data Garmina |
| health: `steps`, `distance_m` | `movement.steps.value`, `.distanceInMeters` | kroki, metry |
| health: `calories_total`, `calories_active`, `calories_resting` | `calories.burnedTotal`, `.burnedActive`, `.burnedResting` | kcal zgłoszone przez Garmin |
| health: `heart_rate_resting`, `heart_rate_min`, `heart_rate_max` | `heartRate.restingValue`, `.minValue`, `.maxValue` | bpm |
| health: `moderate_minutes`, `vigorous_minutes` | `intensityMinutes.moderate`, `.vigorous` | minuty, bez mnożenia vigorous przez 2 |
| health: `stress_avg`, `stress_max` | `stress.avgLevel`, `.maxLevel` | 0–100 |
| health: `body_battery_min`, `body_battery_max`, `body_battery_latest` | `bodyBattery.minValue`, `.maxValue`, `.latestValue` | 0–100 |
| health: `body_battery_charged`, `body_battery_drained` | `bodyBattery.chargedValue`, `.drainedValue` | punkty, sumy zmian w dniu |
| sleep: `started_at`, `ended_at` | `sleepStartTimestampGMT`, `sleepEndTimestampGMT` | epoch ms → UTC |
| sleep: `duration_s`, `nap_s` | `sleepTimeSeconds`, `napTimeSeconds` | sekundy; główny sen nie zawiera drzemek |
| sleep: `deep_s`, `light_s`, `rem_s`, `awake_s`, `unmeasurable_s` | `deepSleepSeconds`, `lightSleepSeconds`, `remSleepSeconds`, `awakeSleepSeconds`, `unmeasurableSleepSeconds` | sekundy |
| sleep: `score` | `sleepScores.overall.value` | 0–100 |
| sleep: `heart_rate_avg`, `stress_avg`, `respiration_avg`, `spo2_avg` | `avgHeartRate`, `avgSleepStress`, `averageRespirationValue`, `averageSpO2Value` | bpm, 0–100, oddechy/min, % |
| hrv: `night_avg`, `weekly_avg`, `status` | `hrvSummary.lastNightAvg`, `.weeklyAvg`, `.status` | wartości źródła, status |
| hrv: `baseline_low`, `baseline_high`, `baseline_low_upper` | `hrvSummary.baseline.balancedLow`, `.balancedUpper`, `.lowUpper` | wartości źródła |

Garmin opisuje HRV jako pomiar w milisekundach w
[materiałach o HRV](https://www.garmin.com/en-SG/blog/general/heart-rate-variability-hrv/).
Sam schemat Personal API nie przypisuje jednostki tym polom; zapisujemy je bez
przeliczania. Przed podpisaniem osi dashboardu trzeba potwierdzić ich zgodność
z dotychczasowym raportem.

### Braki i błędy

Brak pola lub JSON `null` zapisuje SQL `NULL`, również gdy wcześniej była wartość.
Późniejsze uzupełnienie zastępuje ten brak. Zero pozostaje zerem. Odpowiedź HTTP
204 dla snu/HRV oznacza cały zestaw pomiarów `NULL` dla dnia podsumowania zdrowia.
Pusta odpowiedź 200, brak `calendarDate` lub `hrvSummary`, inna data, brak albo
wielokrotne podsumowanie zdrowia, błędny JSON i ujemne pomiary przerywają dzień.
Nie zgadujemy znaczenia liczb ujemnych ani HTTP 404.

Najpierw pobierane i walidowane są wszystkie trzy odpowiedzi dnia, następnie
upserty wykonują się w jednej transakcji. Brak częściowego zapisu dnia.
`IS DISTINCT FROM` pomija zapis niezmienionego rekordu, także przy `NULL`.
Brak własnego `updated_at`, który wymuszałby aktualizacje przy każdym imporcie.

Jeden token na uruchomienie, bez cache, PVC i odświeżania sesji. HTTP ma timeout
20 s i limit odpowiedzi 8 MiB. Dla 429/5xx są maksymalnie trzy próby;
`Retry-After` jest respektowany do 30 s, dłuższy kończy uruchomienie z błędem.
401, pozostałe błędy HTTP, awarie sieci i timeout nie są ponawiane.
Połączenie i transakcja PostgreSQL mają osobne limity 20 s. SIGINT/SIGTERM
anuluje I/O. Logi nie zawierają kluczy, tokenów, DSN ani treści odpowiedzi.

## Publiczny obraz GHCR

Obraz: [`ghcr.io/zewelor/garmin-daily`](https://github.com/users/zewelor/packages/container/package/garmin-daily).
Obsługiwane platformy: `linux/amd64` i `linux/arm64`.

- `latest`: ostatni poprawnie zweryfikowany i opublikowany commit domyślnej gałęzi.
- `<pełny-SHA-commitu>`: obraz konkretnego commitu. Retencja zachowuje 10 ostatnich
  wersji z tagami i zawsze chroni `latest`; starsze wersje mogą zostać usunięte.
- Digest `sha256:…` jest podawany w podsumowaniu zadania publikacji. Przypnij go
  we wdrożeniu, aby aktualizacja `latest` nie zmieniała uruchamianego kodu.

Ustaw `DATABASE_URL` na bazę osiągalną **z kontenera** i `GARMIN_API_KEY` w środowisku
powłoki (lub menedżerze sekretów). `localhost` wewnątrz kontenera wskazuje sam
kontener; do lokalnych prób z PostgreSQL użyj Compose z następnej sekcji.

```sh
docker pull ghcr.io/zewelor/garmin-daily:latest
# Migrację wykonaj tylko raz na pustej bazie/schemacie.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env DATABASE_URL ghcr.io/zewelor/garmin-daily:latest migrate
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env DATABASE_URL --env GARMIN_API_KEY ghcr.io/zewelor/garmin-daily:latest \
  sync --last-days 7 --timezone UTC
```

Dobierz strefę do dat swojego konta Garmin. Obraz nie zawiera kluczy API, ustawień
bazy, danych konta ani plików `.env`. `docker/metadata-action` generuje tagi oraz
etykiety OCI ze źródłem i rewizją; publikacja używa repozytoryjnego `GITHUB_TOKEN`.

Przy pierwszej publikacji maintainer musi ustawić widoczność **pakietu** na
`Public` w ustawieniach GHCR; nowy pakiet jest domyślnie prywatny niezależnie od
widoczności repozytorium. Dopiero anonimowe pobranie manifestu/obrazu potwierdza
publiczny dostęp. [Dokumentacja GHCR](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

## Docker i weryfikacja

```sh
just unit
go vet ./...
just integration
go test ./garmin -run '^$' -fuzz FuzzEpochMilliseconds -fuzztime 5s
just show_dockerignore
just docker-build

# Lokalny, tymczasowy zestaw do prób; Compose odczytuje .env.
docker compose up -d --wait db
docker compose run --rm app migrate
docker compose run --rm app sync --from 2026-10-25 --to 2026-10-31
```

`just integration` zarządza oddzielnym projektem Compose `garmin-daily-integration`: uruchamia
i usuwa jego testową bazę wraz z wolumenem. Port 15432 musi być wolny: zatrzymaj
ręczną bazę deweloperską przed testem. Baza Compose jest **jednorazowa**, nie służy
do zachowania historii.
Testy tworzą i usuwają własne schematy. Można też użyć istniejącej bazy testowej:
`TEST_DATABASE_URL=... go test -tags integration -v ./...`.

Obraz to statyczna binarka i certyfikaty CA w `scratch`, użytkownik 65532.
Nie potrzebuje zapisu do systemu plików ani uprawnień roota.
Kontekst budowania dopuszcza wyłącznie pliki programu, modułu, migracji i licencji.
Docker według [oficjalnego przewodnika początkowego](https://github.com/docker/skills/tree/main/skills/docker-project-foundations),
z zakresem ograniczonym do tego CLI.

Wymagane lokalnie i w CI: format Go, `go vet`, testy HTTP oraz testy na prawdziwym
PostgreSQL: import → identyczny import → korekta → brak pola → uzupełnienie →
błąd w trzeciej tabeli → wznowienie. Dodatkowy test zrywa połączenie do bazy
w trakcie zapisu trzeciej tabeli i sprawdza rollback oraz wznowienie po połączeniu. Test sprawdza też rzeczywisty brak nowej
wersji wiersza przy identycznym imporcie i dokładność dziesiętną.
To test integracyjny z kontrolowanym HTTP; test rzeczywistego Garmina jest
oddzielną granicą dowodu. Go fuzzing sprawdza zachowanie momentu czasu przy dekodowaniu epoch ms.

CI sprawdza format Go, `go vet`, testy jednostkowe i integracyjne oraz budowanie
obrazu i migrację w kontenerze. Pull requesty nie mają uprawnień do publikowania.
Po przejściu weryfikacji push do domyślnej gałęzi publikuje obraz GHCR;
`workflow_dispatch` pozwala powtórzyć cały proces z tej gałęzi.
Renovate używa `.github/renovate.json`, z przeglądem zmian bez automerge.

Wskazówki dla harmonogramów i wdrożeń: [uruchamianie cykliczne](docs/gitops.md).

## Źródła kontraktu

- [Personal API i autoryzacja](https://developer.garmin.com/gc-developer-program/personal/personal-api/)
- [FAQ kluczy i tokenów](https://developer.garmin.com/gc-developer-program/personal/personal-faq/)
- [Oficjalny Swagger](https://developer.garmin.com/gc-developer-program/personal/swagger-docs/)
- [Oficjalny klient Python](https://github.com/garmin/connect-personal-api-python)
- [pgx](https://github.com/jackc/pgx)

Zweryfikowano protokół na rzeczywistym API. Token jest surowym tekstem,
nie obiektem JSON; żądanie tokenu wymaga `Accept: */*` (JSON dawał HTTP 406).
Oficjalny Swagger opisuje znaczniki snu jako string/date-time, ale jego opis
mówi o ms, a rzeczywiste odpowiedzi zawierają liczby epoch ms. Implementacja
obsługuje potwierdzony format liczbowy, bez zgadywania alternatywnych formatów.

## Licencja

[MIT](LICENSE). Kopia licencji znajduje się także w obrazie pod `/LICENSE`.
