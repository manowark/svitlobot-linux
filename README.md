# svitlobot

Клієнт [SvitloBot] для Linux: пінгує хост у домашній мережі (роутер, NAS, ...)
і, поки той відповідає (тобто є світло), шле GET-"серцебиття" на
`https://api.svitlobot.in.ua/channelPing?channel_key=...`. Світло зникло —
серцебиття автоматично припиняються, сервер сам бачить тишу.

Один статичний бінарник, нуль зовнішніх залежностей. Розрахований на роботу
місяцями без обслуговування — LXC, Docker або Raspberry Pi 5.

## Як це працює

```text
svitlobot ──ping──▶ ваш хост у домашній мережі
    ├─ хост відповідає (світло є) ─▶ GET .../channelPing?channel_key=...
    └─ хост не відповідає (світло зник) ─▶ реквести не шлються
```

- Пінг робиться кількома спробами (`attempts`) — одна випадкова втрата
  пакета не рахується як відключення.
- Короткочасні збої мережі не обривають серцебиття одразу — див. `down_threshold`
  нижче.
- Невдалий реквест повторюється на наступних циклах, доки не дійде.
- `mode: "edge"` (опційно) — один GET на появу світла замість постійних
  серцебиттів.
- Перевірка хоста: `icmp` (звичайний пінг) або `tcp` (з'єднання з портом —
  для пристроїв без ICMP; тоді привілеї не потрібні).

## Конфігурація

`/etc/svitlobot/config.json` (приклад — `config.example.json`).

Основні ключі (глобальні для всіх хостів; кожен можна перевизначити у хості):

| Ключ | За замовч. | Опис |
|---|---:|---|
| `interval_seconds` | 50 | як часто пінгувати і слати channelPing |
| `down_threshold` | 3 | скільки підряд невдалих перевірок = IP зник (DOWN) |
| `attempts` / `min_successes` | 4 / 2 | скільки пінгів у перевірці / скільки успіхів = хост доступний |
| `attempt_interval_ms` / `ping_timeout_ms` | 700 / 1000 | пауза і таймаут між спробами |
| `http_timeout_seconds` | 15 | таймаут GET-запиту |
| `web_listen` | `:8080` | адреса статус-сторінки (`""` — вимкнути) |
| `log_file` | — | файл-лог з ротацією; порожньо = тільки journald |
| `status_log_seconds` | 600 | період статус-рядка "все працює" у лог |

Хости (`hosts[]`), обов'язкове тільки `target`:

```json
{
  "interval_seconds": 50,
  "hosts": [
    {
      "name": "router",
      "target": "192.168.1.1",
      "url": "https://api.svitlobot.in.ua/channelPing?channel_key=SECRET"
    }
  ]
}
```

Для `type: "tcp"` додайте `port` (наприклад `443`). Хост з порожнім `url`
пінгується, але реквестів не шле (тільки спостереження на сторінці).

Зміна конфіга без рестарту: `systemctl kill -s HUP svitlobot` або
`docker kill -s HUP svitlobot`.

> `interval_seconds` має бути меншим за таймаут каналу на боці SvitloBot,
> щоб сервер не рахував канал "мертвим" між серцебиттями.

## Анти-флап (`down_threshold`)

Хост вважається доступним, поки перевірки проходять. Перша невдала перевірка
**не** вимикає серцебиття: хост лишається доступним ще `down_threshold - 1`
циклів (дефолт 3). Тільки після N підряд невдалих перевірок — DOWN, реквести
припиняються. Один успішний пінг миттєво повертає хост у доступний стан.
Тобто мережа "блимнула" на 5 с — сервер нічого не помітить.

## Статус-сторінка

- `http://<ip>:8080/` — сторінка стану з автооновленням: по кожному хосту
  бейдж **UP** / **DEGRADED x/y** / **DOWN**, успішність останньої перевірки,
  останній пінг/успіх/серцебиття, HTTP-код або помилка реквесту.
- `http://<ip>:8080/api/state` — той самий стан у JSON.

Сторінка без авторизації — слухайте тільки внутрішню мережу
(`"web_listen": "192.168.1.10:8080"`) або вимикайте (`"web_listen": "-"`).

## Установка

### Docker (compose)

```bash
cp config.example.json config.json && chmod 600 config.json
nano config.json            # target, channel_key

docker compose up -d --build
docker compose logs -f svitlobot
```

- Конфіг монтується read-only — ключ ніколи не потрапляє в образ.
- При `type: "icmp"` контейнеру потрібен `CAP_NET_RAW` (вже прописаний у
  compose); для `type: "tcp"` блок `cap_add` можна прибрати.
- Якщо ICMP з контейнера не проходить — `network_mode: host` у compose
  (тоді блок `ports:` прибрати).

### Готовий образ з GitHub (без білду)

Workflow `.github/workflows/docker-publish.yml` публікує мульти-арх образ
(`amd64` + `arm64`) у GHCR **тільки при пуші тегу релізу**:

- push у `main` → тільки `go vet` + `go test` (CI-гард, без білда образу);
- push тегу `v1.2.3` → збірка і публікація `latest`, `1.2.3`, `1.2`, `1`.

Тому релізи користувачам виходять саме через тег:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

Секрети не потрібні. Після першого прогону зробіть пакет публічним: GitHub →
Packages → `svitlobot` → Package settings → Change visibility → Public. Далі:

```bash
docker run -d --name svitlobot --restart unless-stopped \
  --cap-add NET_RAW --cap-drop ALL \
  -e TZ=Europe/Kyiv \
  -v $(pwd)/config.json:/etc/svitlobot/config.json:ro \
  -p 8080:8080 \
  ghcr.io/<ваш-логін>/svitlobot:latest
```

Або у `docker-compose.yml`: замінити `build: .` на
`image: ghcr.io/<ваш-логін>/svitlobot:latest` — далі як вище.

Теги образу: `latest` — найсвіжіший реліз; `1.2.3` / `1.2` / `1` — тег
`v1.2.3`. Версія тега інжектиться у бінарник (`-X main.version`), тож
статус-сторінка і `svitlobot --version` показують реальний номер релізу.

### systemd (LXC або звичайний Linux)

```bash
make linux            # -> build/svitlobot-linux-amd64  (або linux-arm64)
install -m 755 build/svitlobot-linux-<arch> /usr/local/bin/svitlobot
mkdir -p /etc/svitlobot
cp config.example.json /etc/svitlobot/config.json
chmod 600 /etc/svitlobot/config.json && nano /etc/svitlobot/config.json

cp svitlobot.service /etc/systemd/system/
systemctl enable --now svitlobot
journalctl -u svitlobot -f
```

## Raspberry Pi 5

Потрібна **64-бітна** Raspberry Pi OS або Ubuntu (`uname -m` → `aarch64`).
Найпростіший шлях — готовий arm64-образ з GHCR (розділ вище, `docker compose
pull` сам вибере arm64).

Локальний білд на Pi:

```bash
curl -fsSL https://get.docker.com | sh && sudo usermod -aG docker $USER
cd ~/svitlobot                     # проєкт + config.json з ключем
docker compose up -d
```

Без Docker: `make linux-arm64` на PC → `scp` бінарника на Pi → установка як
systemd-сервіс (розділ вище).

## Логи й оновлення

- Логи: `journalctl -u svitlobot -f` (Docker: `docker compose logs -f -n 100`).
  З `log_file` — плюс файл з ротацією, статус-рядок-"proof" кожні 10 хв.
- Оновлення (systemd): замінити бінарник і `systemctl restart svitlobot`.
- Оновлення (Docker, білд): `docker compose up -d --build`.
- Оновлення (GHCR): `docker compose pull && docker compose up -d`.
