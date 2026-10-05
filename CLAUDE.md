# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## โปรเจกต์นี้คืออะไร

Wallet service (Go + Fiber v2 + GORM + PostgreSQL 15) มี REST endpoint 7 เส้นอยู่ใต้ `/api/v1`
เป็น self-learning project ที่โฟกัสเรื่อง **ความถูกต้องของ database transaction ภายใต้ concurrency**

README เขียนเป็นภาษาไทยและมีเหตุผลเบื้องหลัง design decision ไว้ละเอียด — **อ่านก่อน**
ถ้าจะแก้อะไรที่เกี่ยวกับการจัดการเงิน, row locking หรือ idempotency

## คำสั่งที่ใช้บ่อย

```bash
# รันทั้งระบบ (Postgres + API ที่ :8080)
docker compose up --build

# รัน local โดยใช้แค่ database ของ compose — ค่า default ชี้ไปที่นั้นอยู่แล้ว ไม่ต้อง set env
docker compose up -d db
go run ./cmd/api

# unit test ทั้งหมด (ไม่ต้องมี database)
go test ./... -cover

# รัน test ตัวเดียว / package เดียว
go test ./internal/service/... -run TestTransfer_InsufficientBalance -v

# coverage
go test ./internal/service/... -coverprofile=coverage.out
go tool cover -func=coverage.out

# integration + concurrency + crash test — ต้องมี Postgres, อยู่หลัง build tag
docker compose up -d db
go test -tags=integration ./internal/repository/... -v -count=1

go vet ./...
```

`-count=1` ปิด test cache — ใส่ทุกครั้งที่ต้องการให้ integration test รันจริงไม่ใช่อ่านผลเก่า

schema จัดการด้วย GORM `AutoMigrate` (ใน `repository.NewDatabase`) ไม่ใช่ versioned migration
ดังนั้นถ้าเปลี่ยนชนิดของ column ต้อง `docker compose down -v` เพื่อล้าง volume ก่อน

Postman: `postman/wallet-service.postman_collection.json` — 35 requests เป็น flow เดียวที่กดรันซ้ำได้
(สุ่ม `user_id` / `Idempotency-Key` ใหม่ทุกรอบ ไม่ต้องล้าง DB)

## สถาปัตยกรรม

Clean architecture ที่ dependency ชี้เข้าด้านในจริงๆ ไม่ใช่แค่แบ่งโฟลเดอร์:

```
cmd/api/main.go          wiring, middleware, routes, graceful shutdown
internal/handler         HTTP: validate รูปแบบ request, DTO, map error เป็น status
internal/service         business rules, คุม transaction
internal/domain          entity, เลขคณิตของเงิน, sentinel errors, Repository port
internal/repository      GORM/Postgres adapter + การต่อ database
```

- `domain.Repository` ([internal/domain/repository.go](internal/domain/repository.go)) คือ persistence
  port และอยู่ใน package `domain` — service พึ่ง interface นี้ และ **ไม่ import GORM เลย**
- error ของ GORM ถูกแปลงที่ชั้น repository (`gorm.ErrRecordNotFound` → `domain.ErrWalletNotFound`,
  unique violation ของแต่ละ index → `ErrWalletAlreadyExists` / `ErrDuplicateIdempotencyKey`)
  นี่คือเหตุผลที่ชั้นบนไม่ต้องรู้จัก ORM

### Invariant ที่ต้องรักษาไว้

**เงินเป็น `int64` หน่วยสตางค์** (100 = 1.00 THB) — อย่าเอา `float64` หรือ `decimal` กลับมาใช้กับ balance
จำนวนเงินต่อรายการถูกจำกัดด้วย `domain.MaxAmountSatang`, การบวกต้องผ่าน `domain.AddBalance`
เพื่อกัน int64 overflow, และการแสดงผลใช้ `domain.FormatSatang`
response ของ API ส่งทั้งเลขจำนวนเต็มดิบและ string `*_display` ที่ format ไว้แล้ว

**ทุกการเขียนต้องอยู่ใน `repo.WithTx`** — `WithTx` รับ closure แล้วส่ง `Repository` ที่ผูกกับ transaction
นั้นให้, rollback ให้เองทั้งกรณี error และ panic อย่าเพิ่ม Begin/Commit แบบเขียนมือ
mock ของ service test (`internal/service/mock_repository_test.go`) จะ fail ทันทีถ้ามีการเขียนนอก
`WithTx` ฉะนั้นข้อนี้ถูกบังคับด้วย test จริง

**การล็อกแถวใช้ `GetWalletForUpdate` (`SELECT ... FOR UPDATE`) และ transfer ต้องล็อกตามลำดับ UUID**
ผ่าน `lockPair` ใน [internal/service/wallet_service.go](internal/service/wallet_service.go)
ถ้าล็อกแบบ sender-then-receiver จะ deadlock เมื่อมี A→B และ B→A พร้อมกัน
`SetStatus` ก็ล็อกด้วย เพราะ status เป็นเงื่อนไขของทุกรายการที่ขยับเงิน

**idempotency รับประกันด้วย UNIQUE index ของ Postgres ไม่ใช่ด้วย application code**
ทุก operation ที่ขยับเงินจะ insert แถว ledger **ก่อน** เพื่อให้ `Idempotency-Key` ที่ซ้ำไปชน unique index
ตั้งแต่ก่อนล็อก wallet แล้ว `walletService.finish` จะจับ `ErrDuplicateIdempotencyKey`,
อ่านรายการเดิมกลับมา **นอก** transaction ที่ abort ไปแล้ว แล้วตอบเป็น replay (HTTP 200 แทน 201)
ถ้า key เดิมถูกใช้กับ operation ที่ต่างออกไป จะถูกปฏิเสธด้วย `ErrIdempotencyKeyReuse`
ผ่าน `Transaction.SameOperationAs`
`IdempotencyKey` เป็น `*string` เพราะ Postgres ไม่ถือว่า NULL ชนกันใน unique index

**ตาราง transaction เป็น append-only ledger** — ไม่มีการ update หรือ delete แถว
การแก้รายการผิดทำด้วยการเขียนรายการกลับทาง ทุกแถวเขียนเป็น `COMPLETED` ตรงๆ
และตั้งใจไม่มี state `PENDING` / `FAILED` (ดู design decision ข้อ 5 ใน README)

**ป้องกันหลายชั้น** — guard ในชั้น service มี constraint ของ database คู่กันอยู่
(`chk_wallet_balance_non_negative`, `idx_wallet_user_currency`, unique index ของ idempotency)
ถ้าแก้กฎข้อไหน ต้องแก้ทั้งสองฝั่งให้ตรงกัน

### การจัดการ error

business rule ที่ fail เป็น sentinel error อยู่ใน [internal/domain/errors.go](internal/domain/errors.go)
`respondError` ใน [internal/handler/errors.go](internal/handler/errors.go) คือ **ที่เดียว**
ที่ตัดสินใจเรื่อง HTTP status — ถ้าจะเพิ่ม mapping ใหม่ให้เพิ่มใน `errorTable` ที่นั้น
ห้ามเขียน status ตรงๆ ใน handler
error ที่ไม่มีใน table จะถูก log แล้วตอบ 500 เปล่าๆ โดยไม่ส่งรายละเอียดออกไป

ปัญหาเรื่องรูปแบบ request (UUID ผิด, ไม่มี `user_id`, `reference` ยาวเกิน, currency ไม่รองรับ)
ถูกปฏิเสธใน handler ด้วย `respondBadRequest` และไม่เคยไปถึง service

### ชั้นของ test

unit test (`internal/service`, `internal/handler`, `internal/domain`) รันได้โดยไม่ต้องมี database
ครอบ business decision tree, การใช้ lock, ลำดับการล็อก, เลขคณิตของเงิน และการ map error

สิ่งที่ unit test พิสูจน์ไม่ได้ — การ serialize lock จริง, lost update, deadlock, atomicity ตอน crash —
อยู่ใน test ที่มี `//go:build integration` ที่
[internal/repository/wallet_repository_integration_test.go](internal/repository/wallet_repository_integration_test.go)
ซึ่งจะ `t.Skip` พร้อมบอกวิธีสตาร์ท database ถ้าต่อ Postgres ไม่ได้
ถ้าจะเคลมเรื่อง concurrency หรือ atomicity เพิ่ม ให้เขียนที่ไฟล์นั้น ไม่ใช่ใน test ที่ใช้ mock

## สิ่งที่ตั้งใจไม่ทำ (ระบุไว้ใน README)

ไม่มี auth/authorization, ไม่มี rate limiting, ไม่รองรับการแลกเปลี่ยนสกุลเงิน
(โอนข้ามสกุล → `CURRENCY_MISMATCH`), ไม่มี endpoint สำหรับ reversal,
ไม่มี TTL/cleanup ของ idempotency key, ไม่มี outbox/event publishing,
ไม่มี metrics หรือ tracing นอกจาก request log กับ request id
