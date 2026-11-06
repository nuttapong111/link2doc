# Link2Doc

แปลงหน้าเว็บและไฟล์ PDF เป็น Markdown แล้วส่งเข้า Google Doc (แท็บละลิงก์ / หัวข้อละลิงก์ / Doc ละลิงก์) หรือ Dify Knowledge พร้อมตั้งรอบดึงซ้ำอัตโนมัติ

Backend เขียนด้วย **Go + Gin + GORM** วางโครงแบบ clean architecture ส่วน PDF/OCR เป็นบริการ Python แยก (`pdf-worker/`)

## โครงสร้างโค้ด

```
cmd/link2doc/            จุดเริ่มโปรแกรม ประกอบ dependency ทั้งหมด (MODE=all|api|worker)
internal/
  domain/                entity + interface (ports) ไม่ import ชั้นนอก
  usecase/               ตรรกะหลัก: งาน, ดึง/แปลง, ส่ง Google/Dify, รอบอัตโนมัติ, ตั้งค่า
  repository/postgres/   GORM: repository ทุกตัว, migration, คิวงานบน Postgres
  delivery/http/         Gin router, handler, session cookie
  delivery/worker/       ตัวรันงานเบื้องหลัง + ตัวตั้งเวลา
  infrastructure/
    fetcher/             ดึงเว็บแบบกัน SSRF, จำกัดขนาด/เวลา, เว้นจังหวะต่อโดเมน
    converter/           HTML → Markdown (readability + html-to-markdown), รองรับ TIS-620
    google/              OAuth, Google Docs/Drive, Markdown → คำสั่ง Docs
    dify/                Dify Knowledge API
    pdfworker/           client ของ pdf-worker
    crypto/              AES-256-GCM สำหรับ token ที่เก็บในฐานข้อมูล
pkg/textutil/            ฟังก์ชันช่วยที่ไม่มี dependency
web/static/              หน้าเว็บ (ฝังเข้า binary ด้วย go:embed)
pdf-worker/              Python: PDF → Markdown (PyMuPDF4LLM) + OCR ไทย-อังกฤษ (Tesseract) หรืออ่านด้วย Claude
```

ทิศทาง dependency: `delivery → usecase → domain ← repository / infrastructure` (ประกอบกันใน `cmd/link2doc`)

คิวงานและรอบอัตโนมัติอยู่บน Postgres (`FOR UPDATE SKIP LOCKED`) จึงไม่ต้องมี Redis

## Deploy บน Railway

### 1. Google Cloud (project ใต้องค์กรบริษัท)
1. เปิด **Google Docs API** และ **Google Drive API** (ถ้าจะใช้ "เลือกจาก Drive" เปิด **Google Picker API** ด้วย)
2. OAuth consent screen → User type **Internal**
3. Credentials → OAuth client ID → **Web application** → Authorized redirect URI: `https://<โดเมน web>/auth/google/callback`
4. ถ้าแอดมิน Workspace จำกัดแอปภายนอก ให้เพิ่ม client นี้เป็น Trusted ใน Admin console → Security → API controls

### 2. บริการใน Railway
| บริการ | ที่มา | ตั้งค่า |
|---|---|---|
| `web` | repo นี้ (root) | `railway.json` ชี้ `Dockerfile` → Generate Domain |
| `pdf-worker` | repo นี้ | Root Directory = `/pdf-worker`, Volume ที่ `/data`, `PORT=8000` |
| Postgres | Database | — |

`web` รันทั้ง API และ worker ในตัวเดียว (`MODE=all`) ถ้างานเยอะค่อยแยกเป็นสองบริการ: `MODE=api` กับ `MODE=worker` (ใช้ image เดียวกัน)

### 3. ตัวแปรสภาพแวดล้อม (ดู `.env.example`)
**web**
```
PUBLIC_MODE=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=<รหัสผ่านยาวๆ>
APP_URL=https://<โดเมน web>
DATABASE_URL=${{Postgres.DATABASE_URL}}
SESSION_SECRET=<openssl rand -hex 32>
ENCRYPTION_KEY=<openssl rand -hex 32>
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
ALLOWED_DOMAIN=company.co.th
GOOGLE_DRIVE_SCOPE=full
PDF_WORKER_URL=http://${{pdf-worker.RAILWAY_PRIVATE_DOMAIN}}:8000
PDF_WORKER_TOKEN=<openssl rand -hex 24>
```
**pdf-worker**
```
PORT=8000
PDF_WORKER_TOKEN=<ค่าเดียวกับด้านบน>
DATA_DIR=/data
ANTHROPIC_API_KEY=<ใส่เมื่อจะใช้ "อ่านด้วย Claude">
# CLAUDE_MODEL=claude-haiku-5-5 (อ่าน PDF)  CLAUDE_CLEAN_MODEL=claude-haiku-5-5 (คัดเนื้อหาเว็บ)  CLAUDE_EFFORT=low
```

### อ่านด้วย Claude (วิธีเดียว)
เมื่อใส่ `ANTHROPIC_API_KEY` ให้ pdf-worker ทุกงานจะใช้ Claude Haiku 5.5:
- เว็บ: แปลง HTML เป็น Markdown แล้วให้ Claude ตัดเมนู ส่วนท้าย ลิงก์รอบหน้า เหลือแต่เนื้อหาจริง ถ้อยคำเดิมไม่สรุปไม่แปล (ราว 10 สตางค์ต่อลิงก์)
- PDF: แปลงทุกหน้าเป็นภาพให้ Claude ถอดเป็น Markdown ทั้งข้อความ ตาราง และหน้าสแกน (ราว 10 สตางค์ต่อหน้า)
- ถ้า Claude อ่านหน้าไหนไม่ได้ ระบบใช้วิธีปกติ (ข้อความในไฟล์ / Tesseract OCR) แทนเฉพาะหน้านั้นและแจ้งเตือน ถ้ายังไม่ใส่คีย์ ทั้งระบบใช้วิธีปกติ
- รอบดึงซ้ำอัตโนมัติเรียก Claude เฉพาะเมื่อหน้าเว็บเปลี่ยน
- เปลี่ยนรุ่นได้ด้วย `CLAUDE_MODEL` (PDF) และ `CLAUDE_CLEAN_MODEL` (เว็บ) เช่น `claude-sonnet-5-5` แม่นกว่าแต่ราคาราว 10 เท่า

## พัฒนาในเครื่อง
```bash
cp .env.example .env    # แก้ DATABASE_URL ให้ชี้ Postgres ในเครื่อง, APP_URL=http://localhost:8080, DEV_LOGIN=true
set -a; . ./.env; set +a
go run ./cmd/link2doc
go test ./...
cd pdf-worker && pip install -r requirements.txt && uvicorn app:app --port 8000   # ต้องมี tesseract-ocr-tha
```
`STATIC_DIR=web/static` ให้แก้หน้าเว็บได้โดยไม่ต้อง build ใหม่

## ข้อจำกัดที่รู้
- หน้าเว็บที่ต้องรัน JavaScript ถึงจะมีเนื้อหา ยังไม่รองรับ (ระบบแจ้ง "ไม่พบเนื้อหาในหน้านี้")
- การสร้างแท็บใช้ Docs API `addDocumentTab` ถ้าบัญชีใช้ไม่ได้ ระบบวางเป็นหัวข้อ (Heading 1) ต่อกันแทนและแจ้งในหน้าผลลัพธ์
- ตารางใน Google Doc แสดงเป็นข้อความคั่นด้วย `|` (ตาราง Markdown ใน Dify ครบถ้วน), รายการซ้อนหลายชั้นถูกแปลงเป็นชั้นเดียว
- จำนวนคำภาษาไทยเป็นค่าประมาณ
- การเว้นจังหวะต่อโดเมนนับในแต่ละ process (ถ้ารัน worker หลายตัว แต่ละตัวนับแยกกัน)
- OCR ภาษาไทยด้วย Tesseract อาจผิดวรรณยุกต์บางตัว ระบบเตือน "ควรตรวจคำผิด" ทุกไฟล์ที่ผ่าน OCR
