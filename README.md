# Wallet Service

Backend service สำหรับระบบกระเป๋าเงิน เขียนด้วย Go + Fiber + GORM + PostgreSQL
ออกแบบตาม Clean Architecture โดยเน้นเรื่อง **ความถูกต้องของ Database Transaction ภายใต้ concurrency**

โปรเจกต์นี้เป็น self-learning project ที่ทำเพื่อฝึก 3 เรื่อง:

1. การแยก layer แบบ Clean Architecture ที่ dependency ชี้เข้าด้านในจริงๆ (ไม่ใช่แค่แบ่งโฟลเดอร์)
2. การจัดการเงินให้ถูกต้องแม่นยำ — ทั้งเรื่องชนิดข้อมูล, row locking, และการ retry
3. การพิสูจน์ว่ามันถูกต้องจริง ด้วย test ไม่ใช่ด้วยการกดมือดู

---

## API ทั้งหมด 7 เส้น

| # | Method | Path | คำอธิบาย | Success |
|---|--------|------|----------|---------|
| 1 | `POST` | `/api/v1/wallets` | สร้างกระเป๋าเงินใหม่ | `201` |
| 2 | `GET` | `/api/v1/wallets/:id` | ดูข้อมูลกระเป๋า / **ยอดเงินคงเหลือปัจจุบัน** | `200` |
| 3 | `POST` | `/api/v1/wallets/:id/deposit` | ฝากเงิน | `201` / `200` ถ้าเป็น retry |
| 4 | `POST` | `/api/v1/wallets/:id/withdraw` | ถอนเงิน | `201` / `200` ถ้าเป็น retry |
| 5 | `POST` | `/api/v1/wallets/:id/transfer` | โอนเงินระหว่างกระเป๋า | `201` / `200` ถ้าเป็น retry |
| 6 | `GET` | `/api/v1/wallets/:id/transactions` | ดูประวัติธุรกรรม (มี pagination) | `200` |
| 7 | `PATCH` | `/api/v1/wallets/:id/status` | เปิด / ปิดบัญชี | `200` |

เส้นเสริมสำหรับ ops: `GET /health` (liveness) และ `GET /health/ready` (readiness — ping DB จริง)

> **ยอดเงินคงเหลือ** ใช้เส้นที่ 2 ไม่ได้แยก `/balance` ออกมาต่างหาก เพราะ resource เดียวกันไม่ควรมีสอง representation ให้ต้องคอย sync กัน

---

## เริ่มใช้งาน (Docker)

```bash
docker compose up --build
```

ขึ้นทั้งระบบ: Postgres 15 + API server ที่ `http://localhost:8080`
compose รอ healthcheck ของ Postgres ผ่านก่อนค่อยสตาร์ท API เลยไม่มีปัญหา race ตอนบูต

```bash
curl http://localhost:8080/health          # {"status":"ok"}
curl http://localhost:8080/health/ready    # {"status":"ready"}
```

> ⚠️ **ถ้าเคยรันเวอร์ชันเก่ามาก่อน ต้องล้าง volume ก่อน**
> ```bash
> docker compose down -v && docker compose up --build
> ```
> เพราะเวอร์ชันนี้เปลี่ยนวิธีเก็บเงินจาก `decimal(20,2)` เป็น `bigint` (สตางค์) — อ่านเหตุผลใน [Design decisions](#1-เงินเป็น-int64-หน่วยสตางค์-ไม่ใช่-float64)

### รันแบบ local (ไม่ใส่ container)

```bash
docker compose up -d db     # เอาแค่ database
go run ./cmd/api
```

ค่า default ทั้งหมดชี้ไปที่ Postgres ของ compose อยู่แล้ว ไม่ต้อง set env อะไรเลย

| Env | Default | หมายเหตุ |
|-----|---------|----------|
| `DB_HOST` | `localhost` | ใน container ตั้งเป็น `db` |
| `DB_PORT` | `5432` | |
| `DB_USER` / `DB_PASSWORD` | `user` / `password` | |
| `DB_NAME` | `wallet_db` | |
| `DB_SSLMODE` | `disable` | |
| `DB_TIMEZONE` | `Asia/Bangkok` | มีผลแค่ตอนแสดงผล — ดูหัวข้อ FAQ |
| `DB_LOG_LEVEL` | `warn` | ตั้ง `info` ตอน demo เพื่อโชว์ SQL ทุกคำสั่ง |
| `DB_MAX_OPEN_CONNS` | `25` | |
| `DB_STATEMENT_TIMEOUT_MS` | `5000` | |
| `DB_LOCK_TIMEOUT_MS` | `3000` | |
| `DB_IDLE_IN_TX_TIMEOUT_MS` | `10000` | เก็บกวาด transaction ที่ค้างถือ lock |
| `APP_PORT` | `8080` | |

---

## โครงสร้างโปรเจกต์

```
cmd/api/main.go              wiring + routes + middleware + graceful shutdown
internal/
  config/       config.go              อ่าน env, ประกอบ DSN
  domain/       wallet.go              Wallet entity + WalletStatus
                transaction.go         Transaction entity (append-only ledger)
                money.go               int64 satang, format, overflow guard
                errors.go              sentinel errors
                repository.go          << PORT: interface ที่ service พึ่งพา
  service/      wallet_service.go      business logic ทั้งหมด
  repository/   postgres.go            connection, pool, migration
                wallet_repository.go   << ADAPTER: GORM implementation
  handler/      wallet_handler.go      HTTP controllers
                dto.go                 request/response shapes + validation
                errors.go              error -> HTTP status mapping
```

ทิศทางของ dependency:

```
handler  ──>  service  ──>  domain  <──  repository
                              ▲
                     interface อยู่ตรงนี้
```

`internal/domain` import แค่ standard library กับ `google/uuid` เท่านั้น — ตรวจได้ด้วย

```bash
go list -deps ./internal/domain | grep -v "^wallet-service" | grep -vE "^(internal/|[a-z]+$|[a-z]+/)"
go list -deps ./internal/service | grep gorm    # ต้องไม่เจออะไรเลย
```

`service` ไม่รู้จัก GORM เลยแม้แต่บรรทัดเดียว ทั้งที่ยังใช้ `SELECT ... FOR UPDATE` และ transaction จริง

---

## Design decisions

### 1. เงินเป็น `int64` หน่วยสตางค์ ไม่ใช่ `float64`

```
float64:  0.1 + 0.2 = 0.30000000000000004
int64:    10  + 20  = 30 สตางค์  ✅ ตรงเป๊ะเสมอ
```

`100` = `1.00` บาท ทุกที่ในระบบ

จุดที่คนมักเข้าใจผิด: คอลัมน์ `decimal(20,2)` ใน Postgres **แม่นยำอยู่แล้ว** ปัญหาไม่ได้อยู่ที่ database
แต่อยู่ที่ GORM scan ค่านั้นเข้ามาเป็น `float64` ใน Go — **เงินเพี้ยนที่ชั้น application ไม่ใช่ที่ database**
เปลี่ยนเป็น integer แล้วเลขคณิตถูกต้องโดยโครงสร้าง ไม่ต้องพึ่งการปัดเศษ

API ตอบทั้งสองแบบเสมอ:
```json
{ "balance": 15000, "balance_display": "150.00" }
```
เลข integer คือความจริงที่ระบบใช้คำนวณ ส่วน string มีไว้ให้ client แสดงผลโดยไม่ต้องเอาไปหารเองด้วย float
ฝั่ง request รับเฉพาะ integer — ส่ง `{"amount": 150.5}` มาจะได้ `400 INVALID_AMOUNT` ซึ่งเป็นพฤติกรรมที่ตั้งใจ

### 2. `SELECT ... FOR UPDATE` และการเรียงลำดับล็อก

**บั๊กที่ 1 — lost update:** โค้ดเดิม `TopUp` อ่าน wallet ด้วย `First()` เฉยๆ ไม่ล็อกแถว
ถ้ามีคนฝากเงินพร้อมกัน 2 request:

```
request A: อ่านยอด 100 ──> คำนวณ 100+50=150 ──> เขียน 150
request B: อ่านยอด 100 ─────> คำนวณ 100+50=150 ──────> เขียน 150
                                                    ยอดควรเป็น 200 แต่ได้ 150 → เงินหาย 50
```

แก้โดยใช้ `GetWalletForUpdate` (`SELECT ... FOR UPDATE`) ทุก path ที่แก้ยอดเงิน
request B จะถูกบล็อกรอจน A commit เสร็จ แล้วค่อยอ่านค่าใหม่ที่ถูกต้อง

**บั๊กที่ 2 — deadlock:** ถ้าล็อก sender ก่อน receiver เสมอ

```
A→B ถือ lock A รอ lock B
B→A ถือ lock B รอ lock A     → วนเป็นวงกลม, Postgres ฆ่าตัวหนึ่งทิ้ง (SQLSTATE 40P01)
```

แก้โดยเรียงลำดับล็อกตาม UUID เสมอ ไม่ว่าโอนทิศไหน:

```go
first, second := fromID, toID
if bytes.Compare(first[:], second[:]) > 0 {
    first, second = second, first
}
```

ทั้งสองทิศทางจึงล็อกลำดับเดียวกัน วงจรหายไป deadlock เกิดไม่ได้

### 3. Repository interface + `WithTx`

โจทย์คือ service ต้องคุม transaction กับ row lock ได้ แต่ต้องไม่ import GORM คำตอบคือ port เดียวที่ return ตัวเอง:

```go
type Repository interface {
    WithTx(ctx context.Context, fn func(r Repository) error) error
    GetWalletForUpdate(ctx context.Context, id uuid.UUID) (*Wallet, error)
    // ...
}
```

adapter ฝั่ง GORM ส่ง handle ที่ผูกกับ transaction กลับเข้าไปใน closure:

```go
func (r *gormRepository) WithTx(ctx context.Context, fn func(domain.Repository) error) error {
    return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        return fn(&gormRepository{db: tx})
    })
}
```

**ประเด็นสำคัญ:** pattern นี้ไม่ได้แค่แก้บั๊ก แต่ทำให้บั๊ก 3 แบบ**เขียนไม่ได้อีกเลย**

| บั๊กเดิม | ทำไมถึงเกิดไม่ได้แล้ว |
|---------|----------------------|
| `tx.Commit()` ไม่เช็ค error → commit fail แต่ตอบ 200 | `WithTx` คืน error ของ commit ออกมา ไม่มีทางเผลอทิ้ง |
| ไม่มี `defer rollback` → panic แล้ว transaction ค้างถือ lock | `gorm.Transaction` มี `defer recover()` ให้ในตัว |
| เผลอเขียน log นอก transaction | ทุกอย่างอยู่ใน closure เดียว จะเขียนออกนอกต้องตั้งใจมาก |

และในฝั่ง test ทั้ง transaction machinery เหลือบรรทัดเดียว:

```go
func (m *mockRepo) WithTx(ctx context.Context, fn func(domain.Repository) error) error {
    return fn(m)   // ของจริงคือ BEGIN/COMMIT, ใน test คือ pass-through
}
```

ทำให้เทส business logic ได้ครบทุก branch โดยไม่ต้องมี database

### 4. ถ้าโอนไม่สำเร็จ หรือ server ล่มกลางทาง

คำว่า "ล่มกลางทาง" มี **2 สถานการณ์ที่ต่างกันคนละเรื่อง** และแก้คนละวิธี

#### 4.1 ล่มก่อน COMMIT → Postgres จัดการให้ แต่มีเงื่อนไข

Postgres เขียน WAL แล้ว commit แบบ atomic ถ้า process ตายหรือ connection ขาดก่อน commit
transaction จะถูก rollback ทั้งก้อน — **สภาพ "หักเงินผู้ส่งแล้วแต่ยังไม่เข้าผู้รับ" เป็นไปไม่ได้**
เพราะ Postgres ไม่เคยเปิดเผย state กลางคันให้ใครเห็นเลย ถ้าล่มหลัง commit สำเร็จ ข้อมูลก็อยู่ครบจาก WAL fsync

**เงื่อนไขที่ทำให้การรับประกันนี้เป็นจริง:** การหักเงิน + เพิ่มเงิน + เขียน ledger **ต้องอยู่ใน transaction เดียวกัน**
ซึ่ง `WithTx` ในข้อ 3 บังคับไว้เชิงโครงสร้างแล้ว

**สิ่งที่ทำลายการรับประกันนี้:** `fsync=off` หรือ `synchronous_commit=off` ใน Postgres config
บาง tuning guide แนะนำเพื่อความเร็ว — สำหรับระบบเงินคือห้ามเด็ดขาด

#### 4.2 client ไม่รู้ผลแล้วยิงซ้ำ → Postgres ไม่ช่วย ต้องแก้เอง

เคสที่อันตรายกว่า: **server commit สำเร็จแล้ว แต่ล่มก่อนส่ง response กลับ** (หรือ client timeout ไปก่อน)
client ไม่รู้ว่าสำเร็จหรือไม่ จึงยิงซ้ำ → **เงินเข้า 2 รอบ** โดยที่ database ถูกต้องทุกประการ

แก้ด้วย **Idempotency-Key** (header, optional) — ส่งมากับ deposit / withdraw / transfer ได้

```bash
curl -X POST localhost:8080/api/v1/wallets/$ID/deposit \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000" \
  -d '{"amount": 700}'
```

- ครั้งแรก → `201 Created`
- ยิงซ้ำด้วย key เดิม → **`200 OK` พร้อมรายการเดิม เงินไม่เข้าซ้ำ**
- key เดิมแต่ยอดต่าง → `422 IDEMPOTENCY_KEY_REUSE`

**สิ่งที่รับประกันความถูกต้องคือ UNIQUE index ของ database ไม่ใช่โค้ด Go**

วิธีที่คนมักเขียนคือ `SELECT ดูก่อนว่ามี key นี้ไหม → ถ้าไม่มีค่อย INSERT` ซึ่ง**ผิด** เพราะมี race
ระหว่างสองคำสั่ง ถ้ายิงพร้อมกัน 2 request จะผ่าน SELECT ทั้งคู่แล้ว INSERT ทั้งคู่
UNIQUE index ปิดช่องนี้ที่ระดับ storage engine: ผู้แพ้จะถูกบล็อกรอที่ index จนผู้ชนะ commit เสร็จ
แล้วค่อยได้ SQLSTATE `23505` กลับมา จึงอ่านรายการเดิมเจอแน่นอน

ลำดับใน transaction จึงสำคัญ — **insert ledger ก่อน แล้วค่อยล็อก wallet** ถ้า key ซ้ำจะ abort
ตั้งแต่คำสั่งแรกโดยไม่ทันไปจับ row lock ของใครเลย

> **ไม่ต้องมี endpoint ที่ 8 สำหรับ "เช็คสถานะรายการ"** — client ที่ไม่รู้ผลลัพธ์ ถามระบบด้วยการ
> **ยิงซ้ำด้วย key เดิม** แล้วได้รายการเดิมกลับมาโดยไม่เกิด side effect นี่คือคุณสมบัติของ idempotency เอง

#### 4.3 Invariant check — ตัวพิสูจน์ว่ายังถูกต้อง

integration test ทุกตัวปิดท้ายด้วย `assertWalletConsistent` ที่ตรวจว่า

```
balance == SUM(ฝากเข้า) − SUM(ถอนออก) + SUM(โอนเข้า) − SUM(โอนออก)
```

ถ้ามีจุดไหนที่ยอดเงินกับประวัติไม่ตรงกัน — เงินหาย เงินงอก ledger ขาด ledger เกิน — จะจับได้หมด
เป็นแนวคิดเดียวกับ reconciliation ที่ระบบธนาคารจริงรันทุกวัน

### 5. ทำไมไม่ใช้ PENDING → COMPLETED

`TransactionStatusPending` / `Failed` ประกาศไว้ในโค้ดแต่ไม่ได้ใช้ — **ตั้งใจ**

ใน database เดียว การเขียน `PENDING` ก่อนแล้ว update เป็น `COMPLETED` ใน transaction เดียวกัน
**ไม่ให้ข้อมูลเพิ่มเลย** เพราะทั้งสองอย่าง commit พร้อมกันอยู่ดี ไม่มีใครเห็นสถานะ PENDING ได้เลยสักวินาที

`PENDING` เริ่มมีความหมายก็ต่อเมื่อเงินวิ่งผ่านระบบที่ commit พร้อม database เราไม่ได้ (เช่น payment gateway ภายนอก)
ซึ่งตอนนั้นจะต้องมี outbox pattern + reconciliation job มาด้วย ไม่ใช่แค่เพิ่ม state

### 6. สถานะบัญชีเป็น enum ไม่ใช่ boolean

`"status": "CLOSED"` อ่านรู้เรื่องทั้งใน JSON และใน `psql` ส่วน `"is_active": false` ต้องมาแปลอีกที
และการเพิ่ม `SUSPENDED` ทีหลังเป็นแค่ constant ไม่ใช่การแก้ schema แล้วไล่แก้ boolean ทุกจุด

**ปิดบัญชีที่ยังมีเงินได้** — เงินถูก freeze ไว้ เปิดใหม่แล้วใช้ได้ต่อทันที เป็นการตัดสินใจเชิงธุรกิจ
ไม่ใช่ข้อจำกัดทางเทคนิค (ถ้าอยากบังคับให้ยอดเป็น 0 ก่อนปิด ก็เพิ่ม guard บรรทัดเดียว)

`SetStatus` **ล็อกแถวเหมือน operation ที่แก้เงิน** เพราะ status เป็นเงื่อนไขของ deposit/withdraw/transfer
ถ้าไม่ล็อก จะมีช่องที่ปิดบัญชีสำเร็จพร้อมกับที่เงินกำลังโอนเข้ากระเป๋านั้นพอดี

### 7. การป้องกันหลายชั้น

| ชั้น | กันอะไร |
|-----|---------|
| Handler | รูปร่าง request — UUID พัง, `user_id` ว่างหรือยาวเกิน 100, currency นอก allowlist, `reference` เกิน 255 |
| Service | business rule — ยอดไม่พอ, บัญชีปิด, สกุลเงินไม่ตรง, โอนหาตัวเอง, ยอดเกินเพดาน, overflow |
| Database | `CHECK (balance >= 0)`, `UNIQUE (user_id, currency)`, `UNIQUE (idempotency_key)` |

ชั้น database คือคำตอบของคำถาม "มั่นใจได้ยังไงว่ายอดไม่ติดลบ" — **เพราะไม่ได้พึ่งโค้ดตัวเองอย่างเดียว**
ต่อให้ service มีบั๊ก database ก็ปฏิเสธ (มี integration test ที่ยิง `UPDATE ... balance = -1` ตรงๆ เพื่อพิสูจน์)

### 8. Sentinel errors + mapping ที่เดียว

`respondError()` คือที่เดียวที่ตัดสินใจเรื่อง HTTP status ทั้งระบบ handler ไม่เดาเอง
ซึ่งแก้ปัญหาเดิมที่ "wallet not found" กับ "insufficient balance" ตอบ `400` เหมือนกันหมด

repository แปลง `gorm.ErrRecordNotFound` → `domain.ErrWalletNotFound` ตั้งแต่ชั้นล่างสุด
นี่คือสิ่งที่ทำให้ได้ `404` โดยที่ service กับ handler ไม่ต้องรู้จัก GORM เลย

---

## Error codes

| Code | HTTP | ความหมาย |
|------|------|----------|
| `WALLET_NOT_FOUND` | 404 | ไม่พบกระเป๋า |
| `TRANSACTION_NOT_FOUND` | 404 | ไม่พบธุรกรรม |
| `WALLET_INACTIVE` | 409 | กระเป๋าถูกปิดอยู่ |
| `WALLET_ALREADY_EXISTS` | 409 | user นี้มีกระเป๋าสกุลเงินนี้แล้ว |
| `CURRENCY_MISMATCH` | 409 | โอนข้ามสกุลเงิน (ยังไม่รองรับ) |
| `INSUFFICIENT_BALANCE` | 422 | ยอดเงินไม่พอ |
| `BALANCE_OVERFLOW` | 422 | ยอดหลังทำรายการเกินค่าสูงสุดที่เก็บได้ |
| `IDEMPOTENCY_KEY_REUSE` | 422 | ใช้ key เดิมกับรายการที่ต่างออกไป |
| `INVALID_AMOUNT` | 400 | จำนวนเงินต้องเป็นจำนวนเต็มบวก (สตางค์) |
| `SAME_WALLET` | 400 | โอนหากระเป๋าตัวเอง |
| `INVALID_STATUS` | 400 | สถานะต้องเป็น ACTIVE หรือ CLOSED |
| `INVALID_WALLET_ID` / `INVALID_USER_ID` / `UNSUPPORTED_CURRENCY` / `INVALID_REFERENCE` / `INVALID_REQUEST` | 400 | request ผิดรูปแบบ |
| `INTERNAL_ERROR` | 500 | ข้อผิดพลาดภายใน (รายละเอียดอยู่ใน log ไม่ส่งออกไปให้ client) |

---

## Testing

```bash
# unit tests ทั้งหมด (ไม่ต้องมี database)
go test ./... -cover

# ตัวเลข coverage ของ service layer
go test ./internal/service/... -coverprofile=coverage.out
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html

# integration + concurrency + crash tests (ต้องมี Postgres)
docker compose up -d db
go test -tags=integration ./internal/repository/... -v -count=1
```

`-count=1` ปิด test cache — จำเป็นตอน demo สด ไม่งั้นจะเห็นผลเก่าที่ cache ไว้

### Coverage ปัจจุบัน

| Package | Coverage |
|---------|----------|
| `internal/service` | **90.8%** |
| `internal/handler` | **85.2%** |
| `internal/domain` | 64.7% |

### unit test พิสูจน์อะไรได้ และพิสูจน์อะไรไม่ได้

**พิสูจน์ได้:** business decision tree ครบทุก branch · ทุก mutation เกิดใน `WithTx` จริง
(mock จะ fail ทันทีถ้ามีการเขียนนอก transaction) · ใช้ `FOR UPDATE` ไม่ใช่ read ธรรมดา ·
ลำดับการล็อกเหมือนกันทั้งสองทิศทาง · เลขคณิตของเงิน · การ map error เป็น HTTP status

**พิสูจน์ไม่ได้:** ว่า Postgres serialize lock จริง · isolation level ทำงานยังไง ·
ว่าไม่มี lost update ภายใต้ concurrency จริง · ว่า commit persist จริง
(mock `WithTx` เป็น pass-through ไม่มีแนวคิดเรื่อง rollback เลย)

ส่วนที่ unit test พิสูจน์ไม่ได้ จึงมี integration test 10 ตัวรับช่วงต่อ:

| Test | พิสูจน์อะไร |
|------|------------|
| `TestConcurrentTransfers_NoLostUpdate` | โอน 100 ครั้งพร้อมกัน ยอดรวมคงที่ ไม่มีเงินหาย |
| `TestBidirectionalTransfers_NoDeadlock` | A→B และ B→A 50 คู่พร้อมกัน ไม่มี deadlock |
| `TestConcurrentWithdraw_NoOverdraft` | ยอด 1,000 ยิงถอน 100 พร้อมกัน 20 ครั้ง → สำเร็จ 10 ปฏิเสธ 10 ไม่เคยติดลบ |
| `TestTransferFails_NoPartialState` | โอนพัง 4 แบบ → ยอดทั้งสองฝั่งเท่าเดิม ไม่มี ledger ตกค้าง |
| `TestBackendKilledMidTransaction_NoPartialState` | ฆ่า backend ด้วย `pg_terminate_backend` กลาง transaction → ไม่มี state ครึ่งๆ |
| `TestIdempotency_ConcurrentDuplicateKey` | ยิง 10 request พร้อมกันด้วย key เดียวกัน → เงินเข้าครั้งเดียว ledger แถวเดียว |
| `TestIdempotency_KeyReuseWithDifferentAmount` | key เดิม ยอดต่าง → ปฏิเสธ ยอดไม่ขยับ |
| `TestDBRejectsNegativeBalance` | `UPDATE ... balance = -1` ตรงๆ → database ปฏิเสธ |
| `TestDuplicateWalletRejected` | กระเป๋าซ้ำ (user, currency) → ปฏิเสธ แต่คนละสกุลเงินได้ |
| `TestConcurrentCloseAndTransfer` | ปิดบัญชีพร้อมกับที่โอนเข้า 10 รอบ → ผลลัพธ์เป็นหนึ่งในสองแบบเสมอ |

integration test ใช้ build tag ไม่ใช่ `testing.Short()` เพราะ `-short` เป็น opt-**out** — เพื่อนที่รัน
`go test ./...` โดยไม่มี Postgres จะเจอ test พัง แต่ build tag ทำให้ default run ไม่ compile ไฟล์นั้นเลย
และถ้าเปิด tag แต่ต่อ database ไม่ได้ ก็ `t.Skip` พร้อมบอกวิธีสตาร์ทให้ ไม่ระเบิดใส่หน้า

### Demo: ฆ่า server กลางคัน

```bash
# terminal 1
docker compose up

# terminal 2 — ยิง transfer รัวๆ
while true; do
  curl -s -X POST localhost:8080/api/v1/wallets/$A/transfer \
    -H "Content-Type: application/json" \
    -d "{\"to_wallet_id\":\"$B\",\"amount\":100}" > /dev/null
done

# terminal 3 — ฆ่าทิ้งกลางคัน แล้วเปิดใหม่
docker compose kill api && docker compose up -d api

# แล้วเช็คว่า A+B ยังเท่าเดิม
curl -s localhost:8080/api/v1/wallets/$A | jq .balance
curl -s localhost:8080/api/v1/wallets/$B | jq .balance
```

---

## Postman

import `postman/wallet-service.postman_collection.json` แล้วกด **Run collection**

26 requests เรียงเป็น flow เดียวจบ: สร้างกระเป๋า 2 ใบ → ฝาก → ถอน → โอน → ดูประวัติ →
เทส error 4 แบบ → ปิด/เปิดบัญชี → เทส idempotency (201 แล้ว 200 ด้วย transaction id เดียวกัน) →
เทสกระเป๋าซ้ำและสกุลเงินไม่รองรับ

ทุก request สร้างข้อมูลของตัวเองหรือทำงานบนตัวแปร runtime — **กด Run ซ้ำได้เรื่อยๆ ไม่ต้องล้าง DB**
(`user_id` และ `Idempotency-Key` สุ่มใหม่ทุกรอบด้วย `{{$guid}}`)

ตั้ง `base_url` ได้ที่ collection variables ถ้าไม่ได้รันที่ `localhost:8080`

---

## FAQ

**ทำไมไม่มี authentication เลย ใครก็โอนเงินจากกระเป๋าคนอื่นได้**
อยู่นอกขอบเขตรอบนี้ที่โฟกัสเรื่อง transaction correctness แต่จุดที่มันจะเสียบเข้ามาชัดเจนแล้ว:
middleware ตรวจ JWT → `c.Locals("user_id")` → service เช็คว่า `wallet.UserID` ตรงกับผู้เรียกก่อนทุก mutation
(`ErrForbidden` → 403) โครงสร้างรองรับอยู่แล้วเพราะ `UserID` อยู่บน wallet และ service รับ `ctx` มาแล้ว

**SQL injection ล่ะ**
GORM ใช้ prepared statement ทุก query, `Order("created_at DESC")` เป็น literal ในโค้ดไม่ใช่ input ผู้ใช้,
และไม่มี `Raw`/`Exec` ที่ต่อ string จาก user input เลยแม้แต่ที่เดียว

**รหัสผ่าน database อยู่ใน docker-compose โต้งๆ**
เหมาะกับ demo เท่านั้น ของจริงใช้ Docker secrets / Vault / cloud secret manager
และ compose ควรอ่านจาก `.env` ที่ไม่ commit — `.gitignore` เตรียม `.env` ไว้แล้ว

**ประวัติเป็นล้านแถวจะช้าไหม**
มี pagination (default 20, cap 100) และ index บนทั้ง `from_wallet_id` และ `to_wallet_id`
แต่ query มี `OR` ซึ่ง Postgres จะใช้ BitmapOr รวมสอง index ให้ — ใช้ได้แต่ไม่ใช่ที่เร็วที่สุด
ถ้าโตจริงควรเขียนเป็น `UNION ALL` สองก้อน รัน `EXPLAIN ANALYZE` ดูได้เลย

การเรียงใช้ `ORDER BY created_at DESC, id DESC` — ตัว `id` เป็น tiebreaker ที่จำเป็น
เพราะธุรกรรมที่เกิดรัวๆ อาจมี timestamp ซ้ำกัน ถ้าไม่มีจะทำให้ pagination ซ้ำแถวหรือข้ามแถว

**ทำไม transfer เก็บแถวเดียว ทำไมไม่ทำ double-entry**
รู้จักและจงใจไม่ทำ one-row + `from`/`to` พอสำหรับการโอนภายในสกุลเงินเดียว
ledger จริงใช้ double-entry (ทุกรายการมี debit กับ credit คู่กัน ผลรวมทั้งระบบเป็นศูนย์เสมอ)
ซึ่งจำเป็นเมื่อมีหลายบัญชี หลายสกุลเงิน หรือต้องปิดงบ — invariant ในข้อ 4.3 คือเวอร์ชันย่อของแนวคิดเดียวกัน

**เวลาที่เก็บเป็น timezone อะไร**
Postgres เก็บ `timestamptz` เป็น UTC ภายในเสมอ `TimeZone=Asia/Bangkok` ใน DSN มีผลแค่ตอนแสดงผล
กฎคือเก็บ UTC แสดงตาม client

**ทำไมปิดบัญชีไม่ใช้ soft delete ของ GORM**
`DeletedAt` ทำให้แถวหายไปจาก query ปกติ ซึ่งผิดสำหรับบัญชีที่ปิดแล้วแต่ยังต้องดูยอดและประวัติได้
`status` คือ lifecycle state ที่ตั้งใจให้มองเห็น ไม่ใช่การลบ

**โอนผิดกระเป๋าแล้วแก้ยังไง**
ตาราง `transactions` เป็น **append-only** ไม่มี `UPDATE`/`DELETE` เลย
การแก้ทำด้วยการสร้าง**รายการกลับ (reversal)** ไม่ใช่การแก้ประวัติเดิม (endpoint นี้ยังไม่ได้ทำ)

**ถ้า Postgres ล่มตอนแอปรันอยู่**
pgx pool reconnect ให้เอง request ระหว่างนั้นได้ 500 (ไม่ใช่ข้อมูลผิด),
`/health/ready` ตอบ 503 ให้ load balancer ถอดออกจาก pool, transaction ที่ค้างถูก rollback ตามข้อ 4.1

**ทำไมใช้ AutoMigrate**
เพราะเป็น demo และมันเร็ว แต่ **AutoMigrate ไม่ใช่เครื่องมือ migration ที่ใช้ได้จริงใน production**
มันเปลี่ยน type ได้แต่ไม่รู้จักความหมายของข้อมูล — ตัวอย่างชัดๆ คือรอบนี้เอง:
การเปลี่ยน `decimal(20,2)` → `bigint` จะทำให้ `150.00` บาท กลายเป็น `150` สตางค์ = 1.50 บาท เงินหาย 100 เท่าเงียบๆ
ของจริงต้องเขียน `ALTER TABLE ... TYPE bigint USING (balance * 100)::bigint` ผ่าน golang-migrate
เป็น versioned SQL ที่ review ได้และ rollback ได้ สำหรับ demo นี้เลือกวิธีง่ายคือ `docker compose down -v` แทน

---

## Known limitations / next steps

- **ไม่มี authentication / authorization** — ใครก็เรียก API ได้
- **ไม่มี rate limiting**
- **ไม่รองรับการแลกเปลี่ยนสกุลเงิน** — โอนข้ามสกุลถูกปฏิเสธด้วย `CURRENCY_MISMATCH`
- **ยังไม่มี endpoint สำหรับ reversal** ทั้งที่ออกแบบตารางรองรับไว้แล้ว
- **`idempotency_key` ยังไม่มี TTL หรือ cleanup job** — ตารางจะโตขึ้นเรื่อยๆ ของจริงควรลบ key ที่เกิน 24 ชม.
- **ใช้ AutoMigrate แทน versioned migration**
- **ไม่มี outbox / event publishing** — ถ้าต้องแจ้ง service อื่นเมื่อเงินขยับ ต้องเพิ่ม outbox pattern
- **ไม่มี metrics / tracing** — มีแค่ request log กับ request id

---

## Tech stack

Go 1.24 · Fiber v2 · GORM · PostgreSQL 15 · Docker Compose
