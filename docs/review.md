**Review templatki Go — 11 września 2026**

Ocena: architektura pasuje do założenia małej, łatwej do rozwijania aplikacji
z HTML i htmx. Zostawiłem feature packages, jawne konstruktory i podział
handler → service → repository. Nie ma powodu dodawać frameworka HTTP, ORM,
kontenera DI, generycznych repozytoriów ani frontendowego builda.

Review objęło kod aplikacji, migracje, testy, Docker, justfile, CI,
README/CLAUDE i pięć dokumentów rules. Repozytorium FastAPI posłużyło jako
referencja organizacji i checków; nie zmieniałem jego plików. Poniżej opisuję
stan zastany oraz poprawki wykonane w repozytorium Go.

**Najważniejsze ustalenia i poprawki**

| Priorytet | Stan przed review i skutek | Wprowadzona poprawka |
|---|---|---|
| P1 | `govulncheck` zgłaszał sześć podatności na ścieżkach wywołań: pięć w Go 1.26.5 i jedną w `x/text` 0.37.0. | Go 1.26.8 w go.mod i Dockerfile; `x/text` 0.39.0. Końcowy skan: brak zgłoszeń. |
| P1 | Adapter pgx przekazywał całe `args` do logów. Trafiały tam hashe haseł, adresy email, treść notatek i hashe sesji. Błędy SQL mogły też tworzyć dodatkowy wpis przed logiem warstwy HTTP. | Tracer działa tylko przy debug. Pomija argumenty i payload błędu sterownika; trace ma poziom debug. Test z prawdziwym PostgreSQL obejmuje udane i błędne zapytanie. |
| P2 | `Authenticate` nie sprawdzał limitu hasła, choć rules deklarowały ochronę przed dużym wejściem. `Register` liczył minimum w bajtach: cztery polskie znaki mogły spełnić minimum ośmiu. Parser hashy przyjmował dowolną dodatnią liczbę iteracji i długość klucza. | Limit 512 bajtów przed bazą i PBKDF2 przy loginie; minimum ośmiu znaków Unicode przy tworzeniu konta. Parser wymaga soli 16 B, klucza 32 B i najwyżej 1 200 000 iteracji. Dotychczasowe poprawnie utworzone hashe pozostają obsługiwane. |
| P2 | NUL i błędne UTF-8 przechodziły walidację nazwy/notatki, po czym PostgreSQL odrzucał zapis jako błąd infrastruktury: klient dostawał 500. | Walidacja w serwisach przed SQL. Testy serwisowe i HTTP potwierdzają 422 dla formularzy zwykłych i htmx. |
| P2 | Awaria odczytu sesji była traktowana jak brak zalogowania. Użytkownik dostawał przekierowanie do loginu, a htmx opuszczał bieżący formularz. | Awaria zwraca spójne 500 i zachowuje cookie. Tylko `ErrNoSession` oznacza anonimowego użytkownika. Health i assety nadal omijają odczyt sesji. |
| P2 | Prywatne strony nie miały polityki cache; `/notes` zwracał różne reprezentacje bez `Vary`. Godzinny cache assetów mógł utrzymać stary JS po wdrożeniu. | Dynamiczne odpowiedzi: `no-store`, `Vary: HX-Request, Accept`. Assety bez wersji w URL: `public, no-cache`. |
| P2 | Domyślna konfiguracja htmx próbowała dodawać inline CSS zabroniony przez CSP. Błędy 4xx/5xx poza 422 i problemy sieciowe nie miały komunikatu w interfejsie. | Wyłączone eval, skrypty we fragmentach, inline indicator styles i lokalny cache historii. Dodany mały `static/app.js` z komunikatem błędu, bez nowej zależności. Swapy 422 pozostają aktywne. |
| P2 | `statusRecorder` zapisywał ostatnie `WriteHeader`, chociaż klient otrzymywał pierwsze. Panic omijał format błędów i mógł dopisać błąd do częściowej odpowiedzi 200. | Rejestracja pierwszego końcowego statusu, obsługa niejawnego 200 i flush. Panic przed odpowiedzią przechodzi przez `RespondError`; po rozpoczęciu odpowiedzi przerywa połączenie. |
| P2 | Odblokowanie migratora używało kontekstu bez deadline, a wynik był ignorowany. Brakowało testów własnego migratora. | Ograniczony czas na zwolnienie advisory lock; zamknięcie połączenia przy nieudanym odblokowaniu. Testy równoległych uruchomień, późno dodanego pliku, rollbacku i anulowania. |
| P2 | Każdy login próbował usunąć wszystkie wygasłe sesje w jednym zapytaniu. | Maksymalnie 100 wierszy na próbę porządkowania. Wygaśnięcie nadal jest sprawdzane niezależnie w SQL. |
| P2 | Literówka w poleceniu CLI mogła najpierw połączyć się z bazą i zastosować migracje. `PORT` nie był walidowany; `LOG_LEVEL` akceptował wartości spoza zadeklarowanego zestawu. | Walidacja polecenia i liczby argumentów przed konfiguracją/bazą; sprawdzenie portu i czterech dozwolonych poziomów logów. Serwer jest zamykany także po nieudanym graceful shutdown. |
| P2 | Brak checka złożoności, scanner `@latest`, różnica między lokalnym `ci` i pipeline; CI nie budowało obrazu. | Jawne `types`, gocognit ≤ 10, przypięte wersje narzędzi, vulncheck w `just ci`, build Docker w CI, timeout i anulowanie poprzedniego joba. |
| P3 | Interpolacja argumentów just mogła popsuć nazwę `O'Brien`. Generator migracji dopuszczał niewłaściwą nazwę i nadpisanie pliku. Zwykły Compose uruchamiał też bazę testową, a `compose` nie wymuszało ponownego builda. | Argumenty pozycyjne z cytowaniem, walidacja nazwy i `noclobber`, profil testowy z tmpfs, jawny build stacka developerskiego. |

Zgłoszenia skanera oznaczają osiągalne symbole według analizy statycznej;
nie są dowodem, że wszystkie podatności dało się wykorzystać przez obecne
endpointy. Aktualizacja usuwa ryzyko na poziomie użytego runtime i modułu.
Źródła: [Go releases](https://go.dev/dl/),
[GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091),
[GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090),
[GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089),
[GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088),
[GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972),
[GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970).

Konfiguracja cache i CSP wynika z kontraktu htmx. Szczególnie istotne jest
rozróżnienie pełnej strony i fragmentu dla tego samego URL.
Źródło: [dokumentacja htmx: cache i bezpieczeństwo](https://htmx.org/docs/#caching).
Vendored htmx 2.0.10 jest identyczny bajtowo z plikiem release upstream;
wersję, źródło i checksum zapisałem w [third-party.md](third-party.md).

**Architektura i zbędne elementy**

Podział na pliki w obrębie feature jest trafny. Serwisy user/note odpowiadają
za normalizację i walidację, repozytoria za SQL i ownership, a handlery za
HTTP. `app.New` jest rzeczywistym miejscem kompozycji. Testy pełnego handlera
korzystają z tego samego kodu co produkcja. Oddzielenie auth od user zapobiega
cyklowi importów. Interfejs repozytorium ma tu mały koszt i czytelną granicę;
nie usuwałem go tylko dlatego, że dziś istnieje jedna implementacja.

Poprawne i warte zachowania są też: parametryzacja SQL, UUIDv7 z bazy,
indeks odpowiadający kolejności notatek, ownership w `DELETE`, buforowanie
renderu przed wysłaniem nagłówków, nieprzezroczyste błędy 500, hashowane
sesje, flagi cookie, CrossOriginProtection i działające zwykłe formularze.
Ochrona CSRF wykorzystuje nagłówki przeglądarki, z fallbackiem do Origin;
nie jest mechanizmem uwierzytelnienia klientów API.
Źródło: [net/http.CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection).

Usunąłem nieużywany helper `web.Unauthorized`, nieużywany accessor request ID
z drugim kluczem kontekstu i zbędny generator ID. `crypto/rand.Text` generuje
ID, a `logging.WithAttrs` przechowuje korelację. Długość przyjmowanego ID jest
ograniczona do 64 bajtów. Usunąłem mapowanie poziomów logowania pgx.
Zwykły POST notatki przekierowuje teraz przed zbędnym odczytem listy.
Nagłówek dashboardu nie zawiera imienia, które stawało się nieaktualne po
swapie samego profilu. Widok notatek informuje o limicie 100 wpisów.

Nie uznałem wzorca `rows, _ := pool.Query` za błąd. Sprawdziłem implementację
przypiętego pgx 5.10.0: także przy błędzie pobrania połączenia zwraca on
`errRows`, a Collect propaguje błąd. Test awarii puli dodatkowo sprawdza tę
ścieżkę. Nie należy przenosić tego idiomu na inne API bez sprawdzenia kontraktu.

**Rules: znalezione niespójności**

- Vendored skill zalecał płaski layout, czasem Afero/go-cmp i ograniczenie
  interfejsów, podczas gdy lokalne rules wymagały `internal/`, feature slices
  i jednej zależności. Zapisałem pierwszeństwo lokalnych reguł.
- „Jedyny wyjątek od globals to template” nie odpowiadał sentinel errors,
  embed.FS i regexom w testach. Lista wyjątków jest teraz jawna.
- Globy `http.go` i `postgres.go` pomijały zalecane rozszerzenia takie jak
  `http_share.go`. Reguły obejmują teraz także takie pliki.
- Reguły prefiksów URL nie uwzględniały `/login`, `/me`, `/logout` i `/`.
  Opisałem te konkretne wyjątki.
- Reguła „foreign resource → 404” nie opisywała idempotentnego usuwania
  notatek. Serwis nadal zwraca ErrNotFound, ale HTTP traktuje brak, obce ID
  i powtórne usunięcie jednakowo. To nie daje dostępu do cudzych danych.
- Doprecyzowałem zakres wspólnego formatu błędów: stdlib obsługuje m.in.
  odrzucenie CSRF i błędy assetów. Login ma pełną stronę 401; pozostałe
  formularze obsługują fragmenty 422.
- „Każdy write używa RETURNING” i „każda lista ma LIMIT” wymagały wyjątków
  dla zapisów bez potrzebnego wyniku i katalogów migracji/testowych baz.
- Absolutny zakaz transakcji między feature mógł wymuszać sztuczne łączenie
  domen. Domyślnie transakcja nadal należy do jednej metody repozytorium;
  realny przypadek między domenami wymaga jawnego projektu właściciela
  transakcji. Nie dodałem generycznego unit of work na zapas.
- Poprawiłem twierdzenia, że deploy czyści cache przeglądarki, TTL usuwa
  sesje i timestamp gwarantuje brak kolizji. Żadne z nich nie było prawdą.
- Auto-load reguł zależy od narzędzia agenta. README i CLAUDE wymagają ich
  jawnego przeczytania, gdy narzędzie nie obsługuje takiego mechanizmu.

Nowe komentarze są krótkie i pisane prostym angielskim. Nie przeprowadzałem
masowego przeredagowania starych komentarzy ani certyfikacji zgodności z
całym słownikiem ASD-STE100.

**Dev tooling i porównanie z FastAPI**

| Obszar | FastAPI — referencja | Go po poprawkach |
|---|---|---|
| Format | Ruff format | `just fmt`: gofmt; lint sprawdza brak zmian |
| Analiza statyczna | Ruff | `just lint`: go vet + staticcheck 0.8.1 |
| Typy | Pyright | `just types`: kompilator Go, również kod testów, bez połączenia z bazą |
| Złożoność poznawcza | complexipy ≤ 10 | gocognit 1.2.1 ≤ 10 |
| Testy | pytest + prawdziwy PostgreSQL | go test, prawdziwy PostgreSQL, `-race`, `-count=1` |
| Baza per test | Savepoint/rollback | Osobna baza sklonowana z migrowanego template; dozwolone commity |
| Migracje | Alembic, model drift, round trip | SQL append-only, test atomowości, idempotencji i współbieżności |
| Zależności | Większy stos plus opcjonalne workers/AI | Jedna bezpośrednia zależność Go: pgx; pięć pośrednich modułów; vendored htmx |
| Bezpieczeństwo zależności | Nie kopiowałem całego pipeline | govulncheck 1.8.0 w `just ci` |
| Docker | Build i kontrola runtime | Build w CI; lokalnie także uruchomienie obrazu i smoke test HTTP |

Go nie potrzebuje osobnego odpowiednika Pyright. `go test -race -run '^$'`
kompiluje pakiety i testy, a pusty wybór testów nie wywołuje `testdb.New`.
`go vet` i staticcheck uzupełniają kompilator o dodatkowe klasy błędów.

Limit złożoności obejmuje `cmd`, `internal` i `migrations`, w tym testdb,
ale pomija pliki `_test.go`. To świadoma różnica względem FastAPI: długie,
sekwencyjne asercje testu nie są przepływem produkcyjnej funkcji. Pomiar
przed poprawkami: Migrate 16, handleMeUpdate 15, isUUID 14. Po poprawkach
maksimum wynosi 10, bez wyłączeń dla pojedynczych funkcji i podnoszenia progu.
Źródło metryki i działania `-over`: [gocognit](https://github.com/uudashr/gocognit).

Nie dokładałem golangci-lint obok staticcheck ani drugiej metryki złożoności.
Obecny zestaw ma jasny zakres. Narzędzia są przypięte w justfile i nie
rozbudowują go.mod aplikacji. Baza advisory używana przez vulncheck pozostaje
aktualizowana, więc wynik skanu może się zmienić bez zmiany kodu.

**Weryfikacja wykonanych zmian**

| Check | Wynik |
|---|---|
| Stan bazowy: just ci | PASS; 40 funkcji testowych, brak testów platform/database i platform/web |
| Końcowe just ci | PASS: format, vet, staticcheck, types, complexity, test, vulncheck |
| Testy | 58 funkcji testowych plus subtesty; PostgreSQL 18; `-race -count=1` |
| Cognitive complexity | PASS, maksimum 10 w sprawdzanym kodzie |
| Govulncheck | Przed: 6 zgłoszeń osiągalnych symboli; po: `No vulnerabilities found` |
| go mod verify / go mod tidy -diff | PASS; moduły zweryfikowane, bez driftu |
| just build / docker build | PASS dla binarki hosta i obrazu Linux |
| Uruchomienie obrazu | PASS: startup z migracjami, /healthz, /login, konfiguracja htmx i trzy assety |
| Generator migracji | PASS: zła nazwa odrzucona, istniejący plik nie jest nadpisywany |
| Argumenty just adduser | PASS dla nazwy z apostrofem |
| Vendored htmx | PASS: plik 2.0.10 identyczny z upstream, zapisany SHA-256 |
| JavaScript | `node --check static/app.js` PASS |
| git diff --check | PASS |
| Przeglądarka / DOM / runtime CSP | Niewykonany: brak dostępnej przeglądarki i uprawnień Computer Use |

Nie traktuję httptest ani sprawdzenia składni JavaScript jako testu E2E.
Przed wydaniem zmian frontendowych należy jeszcze przejść w przeglądarce:
login, załadowanie notatek na dashboardzie, zapis profilu, dodanie/usunięcie
notatki, walidację 422, wygaśnięcie sesji i komunikat po błędzie sieciowym.
Trzeba sprawdzić konsolę CSP i powtórzyć przepływy z wyłączonym JS.
Nie uruchamiałem zdalnego GitHub Actions; lokalnie przeszły jego checki i build.

**Świadome ograniczenia i dalsze decyzje**

1. **P1 przed publicznym wdrożeniem: ogranicz POST /login.** Każda poprawnie
   sformatowana próba kosztuje PBKDF2. Limit długości hasła nie ogranicza
   liczby prób. Zostawiłem wskazanie rate limitingu na proxy/bramie;
   nie dodałem nieograniczonej mapy adresów IP ani kolejnej biblioteki.
2. **Sesje:** brak limitu aktywnych sesji użytkownika. Cleanup jest
   oportunistyczny, po loginie, i nie gwarantuje czasu fizycznego usunięcia
   rekordu. Przy wymaganiu retencji potrzebny jest harmonogram. Nie ma
   osobnego systemu resetowania ani zmiany haseł.
3. **Notatki:** lista jest ograniczona do 100 najnowszych wpisów. Starsze
   dane nie znikają z bazy, ale nie ma paginacji. Dla rzeczywistego produktu
   należy dodać kursor `(created_at, id)`, jeśli wszystkie wpisy mają być dostępne.
4. **Migracje:** ledger przechowuje nazwy, nie checksumy plików. Zmiana już
   zastosowanego SQL nie zostanie automatycznie wykryta na produkcji.
   `migrations.Hash` dotyczy wyłącznie testowych baz. Zostawiłem prosty
   migrator i jawną regułę append-only; nie zmieniałem istniejących SQL.
5. **Auth i warstwy:** auth.Service zna ResponseWriter, Request i cookie.
   To jawny adapter sesji przeglądarkowej. Jeśli sesje będą używane poza
   HTTP, wtedy warto wydzielić adapter cookie od operacji tokenowych.
6. **Protokół HTTP:** fallback mux celowo zwraca 404 również dla części
   niedozwolonych metod. JSON jest pomocniczym formatem błędów, a nie pełnym
   API; obecne rozpoznanie Accept nie jest pełną negocjacją wag `q`.
   Nie dokładałem routera ani rozbudowanego mechanizmu content negotiation.
7. **Deployment:** TLS/HSTS, role bazy, backupy i monitoring pozostają
   konfiguracją docelowego środowiska. Startup z migracjami wymaga praw DDL;
   przy rozdzieleniu roli migratora od runtime trzeba też rozdzielić ten etap.
8. **Checki architektury i STE:** importy między feature i styl komentarzy
   wymagają review. Nie ma dedykowanego automatu, który sprawdza te reguły.

Najbardziej opłacalne kolejne rozszerzenia wynikają z potrzeb konkretnej
aplikacji: limit prób logowania, kompletna obsługa kont oraz paginacja.
Obecna baza nie potrzebuje dodatkowych warstw abstrakcji.
