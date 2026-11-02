# Uruchamianie cykliczne

GarminDaily wykonuje jedno polecenie i kończy pracę. Harmonogram zapewnia
zewnętrzny scheduler: cron, timer systemd albo Kubernetes CronJob. Aplikacja
nie wymaga konkretnego operatora PostgreSQL, menedżera sekretów ani GitOps.

## Przygotowanie

1. Przygotuj bazę lub schemat PostgreSQL dla jednego konta Garmin i rolę z prawem
   tworzenia tabel. Każde dodatkowe konto wymaga osobnej bazy lub schematu.
2. Przekaż `DATABASE_URL` i `GARMIN_API_KEY` ze swojego menedżera sekretów.
   Nie zapisuj prawdziwych wartości w repozytorium ani argumentach CLI.
3. Przypnij obraz `ghcr.io/zewelor/garmin-daily` do digestu z podsumowania publikacji.
4. Uruchom `migrate` raz na pustej bazie/schemacie. Ponowna migracja jest błędem;
   nie dodawaj jej do każdego uruchomienia synchronizacji.
5. Uruchom `sync --from YYYY-MM-DD --to YYYY-MM-DD`, sprawdź daty i wyniki,
   a następnie powtórz ten sam zakres. Bez zmian źródła `changed_rows` powinno
   wynosić zero dla każdego dnia.

`DATABASE_URL` może wskazywać schemat przez `search_path`. pgx obsługuje również
zmienne `PGHOST`, `PGUSER` i `PGPASSWORD`, dzięki czemu hasło nie musi być częścią
URL. Dla zdalnej bazy skonfiguruj TLS zgodnie z jej konfiguracją; lokalne
`sslmode=disable` z Compose jest przeznaczone tylko do prób.

## Harmonogram

Uruchamiaj np. `sync --last-days 3 --timezone Europe/Warsaw`, dobierając strefę
IANA do dat konta Garmin. Polecenie pobiera dziś i dwa poprzednie dni; powtarzane
okno pobiera także późniejsze korekty. Długość okna i częstotliwość są decyzją
operatora, a nie ustawieniem zaszytym w obrazie.

Zapewnij jeden proces zapisujący do danych tabel naraz. Dla Kubernetes użyj
`concurrencyPolicy: Forbid`, `restartPolicy: Never`, `backoffLimit: 0` i jawnego
`activeDeadlineSeconds`. Ręczne Joby również uruchamiaj sekwencyjnie. Sprawdzaj
kod zakończenia oraz logi; błąd zatrzymuje zakres, zachowując wcześniejsze dni.
Wznowienie polega na ponownym uruchomieniu zakresu.

Kontener działa jako UID/GID 65532, z systemem plików tylko do odczytu,
bez dodatkowych capabilities. Wymaga DNS, HTTPS do Garmin oraz dostępu do
PostgreSQL. Nie potrzebuje PVC, Service, Ingress ani serwera HTTP.

## Eksploatacja

Ustaw backup PostgreSQL i sprawdź odtworzenie przed poleganiem na zapisanej historii.
Dla dashboardów utwórz osobną rolę tylko do odczytu. Jednostki i mapowanie pól
opisuje [README](../README.md#kontrakt-danych-i-mały-schemat).

Przy zastępowaniu innego importera najpierw porównaj ten sam zakres dat i metryki.
Przełącz harmonogram dopiero po weryfikacji, zachowując dotychczasową historię
oraz możliwość wznowienia starego procesu. Publikacja obrazu nie wykonuje migracji
bazy ani wdrożenia do infrastruktury.
