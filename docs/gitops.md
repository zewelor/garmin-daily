# GarminDaily w GitOps

Plan osobnej zmiany w `/home/omen/personal/gitops`; wdrożenie do klastra nie zostało wykonane.

1. Opublikować obraz GHCR z zatwierdzonego źródła i przypiąć tag oraz digest. Obecne CI tylko testuje i buduje.
2. Dodać `charts/default/garmin-daily/` z natywnym CronJobem app-template, według wzorca Arrtify. ApplicationSet obejmuje już `charts/default/*`.
3. W istniejącym `postgresql-default18` zadeklarować bazę `garmin_daily` przez CNPG Database, rolę właściciela i osobnego czytelnika dla Grafany. Hasła dostarczyć przez VaultSecret, role przez `Cluster.spec.managed.roles`.
4. Początkowo zawiesić CronJob. Uruchomić osobny, jednorazowy Job `migrate` na pustej bazie, a następnie nadać czytelnikowi CONNECT, USAGE i SELECT na trzech tabelach. Migracja nie jest hookiem każdego uruchomienia. Job migracyjny usunąć z Git po potwierdzeniu wykonania, przed aktualizacjami jego obrazu.
5. Ustawić `sync --last-days 3 --timezone Atlantic/Madeira`: dziś i dwie poprzednie daty. Strefa musi odpowiadać kontu Garmin. Początkowo zachować harmonogram starego importera `0 */8 * * *`; okno korekt dobrać po obserwacji danych.
6. Ustawić Forbid, restartPolicy Never, backoffLimit 0 i deadline Jobu. Ręczne Joby również uruchamiać sekwencyjnie. Kontener działa jako UID/GID 65532 z systemem plików tylko do odczytu; nie potrzebuje PVC, HTTP, Service, Ingress ani sond.
7. Dopuścić DNS, HTTPS do Garmin i PostgreSQL przez istniejące capability labels. Dla Grafany dodać wąski egress do CNPG oraz odpowiadający ingress bazy — obecny dostęp Healthchecks nie obejmuje Grafany.
8. W Grafanie dodać PostgreSQL datasource z dedykowanym czytelnikiem i namespace-local sekretem zawierającym tylko jego hasło. Użyć pełnego DNS CNPG między namespace. Dodać mały dashboard dzienny: sen, HRV, Body Battery, tętno i podsumowania; trendy 7/30/90 dni liczyć SQL-em.

## Połączenie CronJoba z bazą

- `GARMIN_API_KEY`: dedykowany Secret aplikacji z Vault.
- `PGHOST`: klucz `host` Secretu `postgresql-default18-app`.
- `PGUSER`: `garmin_daily`.
- `PGPASSWORD`: dedykowany Secret roli.
- `DATABASE_URL`: `postgres:///garmin_daily?sslmode=require`.

pgx uzupełnia parametry z PG*, więc hasła nie trzeba interpolować w URL. Parsowanie konfiguracji i połączenie z lokalnym PostgreSQL sprawdzono; docelowy TLS CNPG wymaga testu w klastrze. Lokalnego `.env` nie publikować.

## Odbiór i przełączenie

Przed publikacją zmian infrastruktury wykonać wymagany przez GitOps niezależny przegląd bazy, sekretów i polityk, render/lint chartów oraz pre-commit.
Po wdrożeniu sprawdzić import jawnego tygodnia, ponowienie bez niepotrzebnych aktualizacji, NULL, jednostki i daty. Porównać dane ze starym raportem i sprawdzić zapytania Grafany. Potwierdzić zarówno ręczny Job, jak i rzeczywiste uruchomienie harmonogramu oraz backup nowej bazy w istniejącym backupie CNPG.

Po akceptacji zawiesić stary garmin-grafana, pozostawiając kompletną definicję, tokenowy PVC i historię QuestDB. Nowe zapisy ma obsługiwać jeden docelowy importer. Rollback: zawiesić GarminDaily i wznowić stary CronJob przez Git, sprawdzając jego uwierzytelnienie. QuestDB nadal obsługuje IoT; usuwanie historii i PVC nie należy do tej zmiany.
