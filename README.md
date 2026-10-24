# svitlobot

Клієнт для [SvitloBot] (`https://api.svitlobot.in.ua/channelPing`) для Linux:
пінгує хост у внутрішній мережі і, поки той відповідає (тобто є світло),
періодично шле GET "серцебиття" на зовнішній ендпоінт з канальним ключем
в URL. Світло пропало — пінги перестали проходити, реквест автоматично
припиняється, сервер сам бачить тишу.

Розрахований на роботу **місяцями без обслуговування**. Один статичний
бінарник, **нуль зовнішніх залежностей** (тільки стандартна бібліотека Go).

## Як це працює

```text
svitlobot ──ping──▶ ваш хост у домашній мережі (роутер, NAS, ...)
    │
    ├─ хост відповідає (світло є) ─▶ GET .../channelPing?channel_key=...
    └─ хост не відповідає (світло зник) ─▶ реквести не шлються
```

- **серцебиття** (дефолт, `mode: "always"`): поки хост на пінги відповідає —
  GET на `channelPing` шлеться кожні `interval_seconds` секунд.
- **захист від випадкових збоїв**: пінг робиться кількома спробами
  (`attempts`), а короткочасні збої мережі не обривають серцебиття одразу
  (`down_threshold`, розділ нижче).
- **надійна доставка**: якщо реквест не пройшов (таймаут, 5xx) — він
  автоматично повторюється на наступних циклах, доки не дійде.
- **режим події** (`mode: "edge"`): один GET на перехід "світла не було →
  з'явилось", замість постійних серцебиттів.
- **варіанти перевірки хоста**: `icmp` (звичайний пінг) або `tcp`
  (з'єднання з портом — для пристроїв без ICMP).

## Стійкість на місяці роботи

- Усі мережеві операції із таймаутами — клієнт не може "зависнути".
- Споживання пам'яті стабільне (у стані спокою ~10–20 MB).
- Помилки у лог пишуться тільки при **зміні** помилки (без лог-спаму).
- Періодичний статус-рядок у лог (`status_log_seconds`) — видно, що процес живий.
- Файловий лог (опційно) з **ротацією** за розміром.
- SIGHUP — **hot-reload конфігу** без рестарту (додати/змінити хости на льоту).
- Автостарт і акуратна зупинка: `Restart=always` у systemd /
  `restart: unless-stopped` у compose, SIGTERM/SIGINT обробляються чисто.

## Конфігурація

`/etc/svitlobot/config.json` (приклад — `config.example.json`):

| Ключ                     | За замовч. | Опис                                                            |
|--------------------------|-----------:|-----------------------------------------------------------------|
| `interval_seconds`       | 50         | період перевірки: як часто пінгувати і слати channelPing     |
| `attempts`               | 4          | скільки пінгів у одній перевірці (1..20)                                  |
| `attempt_interval_ms`    | 700        | пауза між спробами пінгу                                      |
| `ping_timeout_ms`        | 1000       | таймаут одного пінга                                            |
| `min_successes`          | 2          | скільки успішних пінгів = хост доступний                               |
| `down_threshold`         | 3          | скільки підряд невдалих перевірок = IP зник (DOWN), див. анти-флап |
| `http_timeout_seconds`   | 15         | таймаут GET-запиту                                              |
| `status_log_seconds`     | 600        | як часто писати у лог рядок "все працює"                        |
| `web_listen`             | `:8080`    | адреса статус-сторінки (`""` або `"-"` — вимкнути)               |
| `log_file`               | —          | шлях до лог-файла; порожньо = тільки journald                   |
| `log_max_size_mb`        | 10         | розмір лог-файла до ротації                                     |
| `log_max_backups`        | 3          | скільки ротайованих файлів тримати                               |
| `hosts`                  | —          | масив хостів (див. нижче)                                       |

Кожен хост (`hosts[]`); усі поля крім `target` — опційні і беруться з
глобального рівня:

| Ключ                | За замовч. | Опис                                                                     |
|---------------------|-----------:|--------------------------------------------------------------------------|
| `name`              | авто       | унікальне ім'я (ключ hot-reload)                                         |
| `target`            | —          | IP або hostname внутрішнього хоста                                       |
| `type`              | `icmp`     | `icmp` або `tcp`                                                         |
| `port`              | —          | (тільки для `tcp`) порт, куди конектитись                                |
| `url`               | —          | `https://api.svitlobot.in.ua/channelPing?channel_key=ВАШ_КЛЮЧ`            |
| `mode`              | `always`   | `always` = серцебиття після кожної успішної перевірки;
  `edge` = один GET на перехід DOWN→UP |
| `down_threshold`    | 3          | (пер-хост) скільки невдалих перевірок поспіль = IP зник                      |
| параметри бекства   | глобальні  | `interval_seconds`, `attempts`, `attempt_interval_ms`, `ping_timeout_ms`, `min_successes`, `down_threshold`, `http_timeout_seconds`, `status_log_seconds` |

Мінімальний конфіг:

```json
{
  "interval_seconds": 50,
  "hosts": [
    {
      "target": "192.168.1.1",
      "url": "https://api.svitlobot.in.ua/channelPing?channel_key=SECRET"
    }
  ]
}
```

Після зміни конфіга: `systemctl kill -s HUP svitlobot` або
`docker kill -s HUP svitlobot` (без рестарту, без втрати стану).

> Таймінг: щоб сервер не рахував канал "мертвим" між серцебиттями,
> `interval_seconds` має бути меншим за таймаут каналу на боці SvitloBot.

## Анти-флап: коли ми вважаємо, що IP зник (`down_threshold`)

Короткострокові мережеві збої не повинні миттєво обривати серцебиття (сервер
SvitloBot рахує канал мертвим, коли реквест не приходить). Тому:

- Хост вважається **UP (доступний)**, поки перевірки проходять (успішних пінгів ≥ `min_successes`).
- Якщо перевірка повністю провалилась, хост залишається доступним ще
  `down_threshold - 1` невдалих перевірок (дефолт 3): серцебиття продовжує йти,
  ніби нічого не сталося.
- Тільки після `down_threshold` підряд невдалих перевірок хост переходить у
  **DOWN** — реквести на channelPing припиняються, і сервер через свій таймаут
  каналу реєструє відключення.
- Один успішний пінг у будь-якій перевірці повертає хост у доступний стан.

Тобто поріг працює так: **IP вважаємо зниклим тільки якщо не пінгується
N циклів поспіль**. Мережа "блимнула" на 5 с — серцебиття продовжує йти.

## Статус-сторінка

За замовчуванням увімкнена (`web_listen: ":8080"`; `""` або `"-"` вимикає).
Зміна адреси — SIGHUP, сервер перезапуститься без рестарту процесу.

- `http://<ip>:8080/` — **сторінка стану (англійською)** з автооновленням кожні
  5 с: чіпи з версією/uptime/часом старту, зведення по всіх хостах (hosts /
  up / degraded / down), і по кожному хосту карточка кольоровим бейджем
  **UP** / **DEGRADED x/y** (grace анти-флапу) / **DOWN**, успішність
  останньої перевірки, час останнього пінгу/успіху/серцебиття (HTTP-код
  або помилка), лічильник fail streak.
- `http://<ip>:8080/api/state` — той самий стан у JSON (для скриптів і
  моніторингу).

⚠️ Сторінка без авторизації — слухайте тільки внутрішню мережу
(`"web_listen": "192.168.1.10:8080"`) або вимикайте (`"-"`), якщо не потрібна.

## Білд

```bash
# під Linux (нативно):
make build            # -> ./svitlobot

# крос-компіляція з PC:
make linux            # -> build/svitlobot-linux-amd64
make linux-arm64      # -> build/svitlobot-linux-arm64 (Raspberry Pi 5 і т.п.)

file build/svitlobot-linux-arm64
# ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped
```

Перевірка: `go test ./...` — юніт-тести + race-перевірка.

## Установка як systemd-сервіс (LXC або стандартний Linux)

```bash
# 1. бінарник і конфіг
install -m 755 build/svitlobot-linux-amd64 /usr/local/bin/svitlobot
mkdir -p /etc/svitlobot
cp config.example.json /etc/svitlobot/config.json
chmod 600 /etc/svitlobot/config.json
nano /etc/svitlobot/config.json     # вписати target/channel_key

# 2. systemd-одиниця
cp svitlobot.service /etc/systemd/system/
systemctl daemon-reload

# 3. запуск
systemctl enable --now svitlobot

# 4. перевірити
systemctl status svitlobot
journalctl -u svitlobot -f
```

## Docker

Крім LXC/systemd, клієнт можна запустити як Docker-контейнер (наприклад,
на хості, де вже працює Docker, або всередині LXC з увімкненим nesting).

```bash
# 1. конфіг (у репозиторії його немає — там ключ channel_key)
cp config.example.json config.json
chmod 600 config.json
nano config.json          # target, channel_key, web_listen, ...

# 2. білд і старт
docker compose up -d --build

# 3. логи і статус
docker compose logs -f svitlobot
curl http://127.0.0.1:8080/api/state
```

Що важливо:

- **ICMP**: контейнер працює від root з обмеженим набором капів
  (`CAP_NET_RAW` потрібен тільки для `type: "icmp"`). Якщо всі хости
  в конфігу `type: "tcp"` — блок `cap_add` можна видалити.
- **Конфіг** монтується read-only (`./config.json:/etc/svitlobot/config.json`);
  ключ `channel_key` ніколи не потрапляє в образ.
- **Стоп**: `docker stop` шле SIGTERM — клієнт завершується чисто
  (`stop_grace_period: 20s`).
- **Мережа**: default bridge зазвичай має доступ до внутрішньої мережі.
  якщо ICMP до таргета з контейнерної мережі не проходить —
  `network_mode: host` у compose (тоді блок `ports:` прибрати).
- **healthcheck** на `/api/state`: якщо вимкнули статус-сторінку в конфігу
  (`"web_listen": "-"`), закоментуйте блок `healthcheck` у compose.

## Готовий образ з GitHub (GHCR) — без білду на своєму хості

У репозиторії є workflow `.github/workflows/docker-publish.yml`, який на кожен
push у `main` і кожен тег `vX.Y.Z` автоматично:

1. проганяє `go vet` + `go test`;
2. збирає мульти-арх образ (`linux/amd64` + `linux/arm64` — Raspberry Pi 5
   підтримується з коробки);
3. публікує його в GitHub Container Registry.

Що потрібно з вашого боку: просто запушити репозиторій на GitHub (з гілкою
`main`) — все спрацьовує з дефолтним `GITHUB_TOKEN`, жодних секретів додавати
не треба. Оціните прогін: вкладка **Actions → docker-publish**.

Теги образу (`ghcr.io/<ваш-логін>/svitlobot`):

| Тег | Коли оновлюється |
|---|---|
| `latest` | кожен push у `main` |
| `main` | кожен push у `main` |
| `1.2.3`, `1.2`, `1` | пуш тегу `v1.2.3` |
| `sha-abc1234` | кожен білд (точний коміт) |

Перший прогін створює пакет **приватним**. Щоб витягувати без `docker login`:

1. GitHub → ваш профіль → **Packages** → `svitlobot` → **Package settings**
2. **Danger Zone → Change visibility → Public**

### Використання готового образу

```bash
# 1. конфіг поруч
cp config.example.json config.json && chmod 600 config.json
nano config.json

# 2. запускаємо прямо з GHCR (замість docker compose build)
docker run -d --name svitlobot --restart unless-stopped \
  --cap-add NET_RAW --cap-drop ALL --security-opt no-new-privileges:true \
  -e TZ=Europe/Kyiv \
  -v $(pwd)/config.json:/etc/svitlobot/config.json:ro \
  -p 8080:8080 \
  ghcr.io/<ваш-логін>/svitlobot:latest

# оновлення до нової версії — просто перетягнути і перестворити
docker pull ghcr.io/<ваш-логін>/svitlobot:latest
docker rm -f svitlobot && <повторити docker run вище>
```

Або через `docker compose` — замініть у `docker-compose.yml` блок `build: .`
на готовий образ:

```yaml
services:
  svitlobot:
    image: ghcr.io/<ваш-логін>/svitlobot:latest
    # build: .          # білд на місці не потрібен
```

```bash
docker compose up -d        # сам зробить pull
docker compose pull && docker compose up -d   # оновлення
```

На Raspberry Pi 5 це стає найпростішим шляхом (див. розділ Pi 5, Шлях A/B) —
арmeler-64 образ знаходиться в тому ж мульти-архі, `docker compose pull`
сам вибере arm64.

## Raspberry Pi 5 (arm64): покрокова збірка образу

Raspberry Pi 5 — це `aarch64` (arm64). Клієнт під нього працює без змін,
а arm64-образ можна збирати і прямо на Pi, і з PC.

**Передумови (для всіх шляхів):** Raspberry Pi 5 з **64-бітною** Raspberry
Pi OS (Bookworm) або Ubuntu arm64. Перевірка: `uname -m` має повертати
`aarch64`. Якщо повертає `armv7l` — встановлена 32-бітна система: Pi 5
не працює на 32-бітному ядрі. Переустановіть систему через Raspberry Pi
Imager, вибравши 64-бітний образ "Raspberry Pi OS (64-bit)" — підійде і Lite.

### Шлях A: все будуємо прямо на Pi 5 (найпростіший)

1. Встановіть Docker на Pi:

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER && newgrp docker
sudo systemctl enable --now docker
```

2. Перенесіть проєкт на Pi (з PC; без `build/`, логів і конфіга з ключем):

```bash
rsync -av --exclude build --exclude '*.log' --exclude config.json \
      ./svitlobot/ user@raspberrypi:~/svitlobot/
```

3. Створіть конфіг з вашим ключем:

```bash
cd ~/svitlobot
cp config.example.json config.json
chmod 600 config.json
nano config.json    # вписати target та channel_key
```

4. Зберіть образ:

```bash
docker compose build      # або: docker build -t svitlobot-client:latest .
```

5. Запустіть і перевірте:

```bash
docker compose up -d
docker compose logs -f svitlobot      # очікуємо "started host ..."
curl http://127.0.0.1:8080/api/state  # JSON стану
```

Статус-сторінка з іншої машини: `http://<ip-pi5>:8080/`.

6. Автостарт після reboot: `restart: unless-stopped` вже прописаний у
   `docker-compose.yml`, а Docker сам стартує як системний сервіс —
   більше нічого робити не треба.

7. Оновлення:

```bash
# перенести оновлені вихідники з PC, потім:
cd ~/svitlobot
docker compose up -d --build
docker image prune -f    # прибрати старі невикористані образи
```

### Шлях B: образ збираємо на PC, запускаємо на Pi 5

Корисно, якщо на Pi мало місця, повільна SD-карта або не хочете
тримати на ній Go-тулчейн.

1. На PC збудуйте arm64-образ і перейліть його на Pi одним пайпом:

```bash
docker buildx build --platform linux/arm64 -t svitlobot-client:arm64 --load .
docker save svitlobot-client:arm64 | ssh user@raspberrypi 'docker load'
```

(те саме у Makefile: `make docker-arm64`)

2. На Pi у `~/svitlobot/docker-compose.yml` приберіть секцію `build:`
   (образ уже локальний), залишивши:

```yaml
services:
  svitlobot:
    image: svitlobot-client:arm64
```

поруч покладіть `config.json` з ключем. Далі як у Шляху A, кроки 5–7
(`docker compose up -d` тощо).

Альтернатива передачі через `docker save | ssh`: запуште образ у registry
(Docker Hub або локальний registry) і на Pi замість збірки зробіть
`docker compose pull`.

### Шлях C: без Docker — статичний бінарник + systemd на Pi 5

Класичний шлях, ідентичний LXC-інструкції вище:

1. Збираємо на PC (крос-компіляція; на Pi Go не потрібен):

```bash
make linux-arm64    # -> build/svitlobot-linux-arm64
file build/svitlobot-linux-arm64
# ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped

scp build/svitlobot-linux-arm64 user@raspberrypi:/tmp/svitlobot
```

2. Установка на Pi:

```bash
sudo install -m 755 /tmp/svitlobot /usr/local/bin/svitlobot
sudo mkdir -p /etc/svitlobot
sudo nano /etc/svitlobot/config.json       # вписати target/channel_key
sudo cp svitlobot.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now svitlobot
journalctl -u svitlobot -f
```

(Або зібрати прямо на Pi: `sudo apt install -y golang-go && make build`)

### Перевірка, що все під arm64

```bash
uname -m                    # aarch64
docker inspect svitlobot --format '{{.Architecture}}'   # arm64
curl -s localhost:8080/api/state | head     # процес живий
```

Примітки:

- Raspberry Pi OS Lite (64-bit) працює так само, як повний Desktop — важливий
  лише 64-бітний користувацький простір.
- Ліміти Docker (`mem_limit`/`cpus`) можна не ставити: клієнт у стані
  спокою займає ~10–20 MB RAM.
- Статус-сторінка на хості: за замовчуванням `ports: "8080:8080"`; або
  `network_mode: host` у compose (тоді `web_listen` слухає прямо на Pi,
  і блок `ports` треба прибрати).

## ICMP-привілеї (важливо для LXC/Docker)

Raw ICMP-сокет потребує `CAP_NET_RAW`:

- Якщо процес працює від **root** (стандартно у LXC і в Docker-контейнері) —
  у привілейованому LXC працює "з коробки"; у непривілейованому теж
  найчастіше працює (CAP_NET_RAW діє всередині user namespace контейнера).
  У Docker капа видається через `cap_add: [NET_RAW]`.
- Якщо працює не від root або сокет недоступний — клієнт **сам** переходить
  на системну утиліту `ping` (у Debian/Ubuntu/Raspberry Pi OS вона setuid
  root і працює завжди).
- Якщо ICMP повністю заборонений політиками — використовуйте `type: "tcp"`
  з портом тієї служби, яку пристрій точно слухає (роутер: 443/80;
  NAS: 445/22 і т.д.).

## Логи

- Без `log_file` — усе у journald: `journalctl -u svitlobot -f`
  (Docker: `docker compose logs -f svitlobot`).
- З `log_file: "/var/log/svitlobot.log"` — лог одночасно у journald **і** у
  файл з ротацією (`svitlobot.log.1..3`). Статус-рядок кожні 10 хв показує,
  що процес живий.

## Розгортання оновлення

```bash
# systemd:
systemctl stop svitlobot
install -m 755 build/svitlobot-linux-<arch> /usr/local/bin/svitlobot
systemctl start svitlobot

# docker (білд на місці):
docker compose up -d --build

# docker (ready образ з GHCR):
docker compose pull && docker compose up -d
```

Стан хостів живе в пам'яті: після старту клієнт один раз пройде цикл;
якщо хост виявиться UP — серцебиття піде (це коректно і бажано).
