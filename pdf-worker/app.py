"""pdf-worker: PDF -> Markdown with Thai OCR for scanned pages.

Called only by the Link2Doc web/worker services over Railway's private network.
Files are stored on a Railway volume mounted at DATA_DIR.
"""
import ipaddress
import os
import re
import socket
import time
import uuid
from collections import Counter
from pathlib import Path
from urllib.parse import urlparse

import fitz  # PyMuPDF
import httpx
import pymupdf4llm
from fastapi import Depends, FastAPI, File, Header, HTTPException, UploadFile
from pydantic import BaseModel

DATA_DIR = Path(os.environ.get("DATA_DIR", "/data"))
DATA_DIR.mkdir(parents=True, exist_ok=True)
TOKEN = os.environ.get("PDF_WORKER_TOKEN", "")
MAX_BYTES = int(os.environ.get("MAX_PDF_MB", "500")) * 1024 * 1024
FILE_TTL_DAYS = int(os.environ.get("FILE_TTL_DAYS", "30"))
MIN_TEXT_CHARS = 40  # a page with less extractable text than this is treated as scanned
OCR_DPI = int(os.environ.get("OCR_DPI", "300"))
ANTHROPIC_API_KEY = os.environ.get("ANTHROPIC_API_KEY", "")
# PDF pages: Sonnet reads Thai scans well at half Opus's price. Web cleanup is plain text work: Haiku is enough.
CLAUDE_MODEL = os.environ.get("CLAUDE_MODEL", "claude-sonnet-5-5")
CLAUDE_CLEAN_MODEL = os.environ.get("CLAUDE_CLEAN_MODEL", "claude-haiku-5-5")
CLAUDE_EFFORT = os.environ.get("CLAUDE_EFFORT", "low")  # transcription needs little reasoning
CLAUDE_DPI = int(os.environ.get("CLAUDE_DPI", "150"))
_claude = None

app = FastAPI(title="link2doc pdf-worker")


def auth(authorization: str = Header(default="")):
    if TOKEN and authorization != f"Bearer {TOKEN}":
        raise HTTPException(401, "unauthorized")


def path_for(file_id: str) -> Path:
    if not re.fullmatch(r"[0-9a-f]{32}", file_id):
        raise HTTPException(400, "bad file id")
    p = DATA_DIR / f"{file_id}.pdf"
    if not p.exists():
        raise HTTPException(404, "ไม่พบไฟล์ (อาจหมดอายุแล้ว)")
    return p


def open_pdf(p: Path) -> fitz.Document:
    try:
        return fitz.open(p)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(422, f"เปิดไฟล์ PDF ไม่ได้: {e}") from e


def cleanup_old_files():
    cutoff = time.time() - FILE_TTL_DAYS * 86400
    for f in DATA_DIR.glob("*.pdf"):
        if f.stat().st_mtime < cutoff:
            f.unlink(missing_ok=True)
    for f in DATA_DIR.glob("*.name"):
        if not (DATA_DIR / (f.stem + ".pdf")).exists():
            f.unlink(missing_ok=True)


@app.on_event("startup")
def _startup():
    cleanup_old_files()


@app.get("/healthz")
def healthz():
    return {"ok": True, "claude": bool(ANTHROPIC_API_KEY)}


def save_meta(file_id: str, name: str):
    (DATA_DIR / f"{file_id}.name").write_text(name, encoding="utf-8")


def read_name(file_id: str) -> str:
    p = DATA_DIR / f"{file_id}.name"
    return p.read_text(encoding="utf-8") if p.exists() else f"{file_id}.pdf"


def finish_saved(file_id: str, name: str):
    p = DATA_DIR / f"{file_id}.pdf"
    doc = open_pdf(p)
    pages = doc.page_count
    doc.close()
    save_meta(file_id, name)
    return {"file_id": file_id, "name": name, "pages": pages}


@app.post("/files", dependencies=[Depends(auth)])
async def upload(file: UploadFile = File(...)):
    file_id = uuid.uuid4().hex
    dest = DATA_DIR / f"{file_id}.pdf"
    size = 0
    with dest.open("wb") as out:
        while chunk := await file.read(1024 * 1024):
            size += len(chunk)
            if size > MAX_BYTES:
                out.close()
                dest.unlink(missing_ok=True)
                raise HTTPException(413, f"ไฟล์ใหญ่เกิน {MAX_BYTES // 1024 // 1024} MB")
            out.write(chunk)
    with dest.open("rb") as f:
        if f.read(5) != b"%PDF-":
            dest.unlink(missing_ok=True)
            raise HTTPException(415, "ไฟล์นี้ไม่ใช่ PDF")
    return finish_saved(file_id, file.filename or f"{file_id}.pdf")


def assert_public(url: str):
    u = urlparse(url)
    if u.scheme not in ("http", "https") or not u.hostname:
        raise HTTPException(400, "ลิงก์ไม่ถูกต้อง")
    for info in socket.getaddrinfo(u.hostname, None):
        ip = ipaddress.ip_address(info[4][0])
        if ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved or ip.is_multicast:
            raise HTTPException(400, "ไม่อนุญาตให้ดึงที่อยู่ภายใน")


class FromUrl(BaseModel):
    url: str


@app.post("/files/from-url", dependencies=[Depends(auth)])
def from_url(body: FromUrl):
    url = body.url
    file_id = uuid.uuid4().hex
    dest = DATA_DIR / f"{file_id}.pdf"
    with httpx.Client(follow_redirects=False, timeout=60) as client:
        for _ in range(5):
            assert_public(url)
            with client.stream("GET", url, headers={"user-agent": "Link2DocBot/0.1"}) as r:
                if r.is_redirect:
                    url = str(r.next_request.url) if r.next_request else r.headers["location"]
                    continue
                if r.status_code != 200:
                    raise HTTPException(502, f"ดาวน์โหลด PDF ไม่สำเร็จ ({r.status_code})")
                size = 0
                with dest.open("wb") as out:
                    for chunk in r.iter_bytes(1024 * 1024):
                        size += len(chunk)
                        if size > MAX_BYTES:
                            out.close()
                            dest.unlink(missing_ok=True)
                            raise HTTPException(413, "ไฟล์ใหญ่เกินกำหนด")
                        out.write(chunk)
                break
        else:
            raise HTTPException(502, "redirect มากเกินไป")
    name = Path(urlparse(url).path).name or f"{file_id}.pdf"
    return finish_saved(file_id, name)


@app.get("/files/{file_id}/info", dependencies=[Depends(auth)])
def info(file_id: str):
    doc = open_pdf(path_for(file_id))
    scanned = [i + 1 for i, page in enumerate(doc) if len(page.get_text("text").strip()) < MIN_TEXT_CHARS]
    title = (doc.metadata or {}).get("title") or ""
    out = {"pages": doc.page_count, "scanned_pages": scanned, "title": title, "name": read_name(file_id)}
    doc.close()
    return out


class ConvertReq(BaseModel):
    pages: list[int]
    ocr: bool = True
    lang: str = "tha+eng"
    strip_headers: bool = True
    page_markers: bool = True
    # tesseract: OCR scanned pages; claude: Claude reads scanned pages; claude_all: Claude reads every page
    engine: str = "tesseract"


def ocr_page(page: fitz.Page, lang: str) -> str:
    import pytesseract
    from PIL import Image

    pix = page.get_pixmap(dpi=OCR_DPI, colorspace=fitz.csGRAY)
    img = Image.frombytes("L", (pix.width, pix.height), pix.samples)
    text = pytesseract.image_to_string(img, lang=lang, config="--psm 3")
    # Common OCR split of sara am (nikhahit + sara aa) back into one character.
    text = text.replace("ํา", "ำ")
    # Tesseract inserts spaces between Thai characters on some fonts; collapse them.
    text = re.sub(r"(?<=[฀-๿]) (?=[฀-๿])", "", text)
    paras = [re.sub(r"\s*\n\s*", " ", p).strip() for p in re.split(r"\n\s*\n", text)]
    return "\n\n".join(p for p in paras if p)


CLAUDE_PROMPT = """This image is one page of a PDF document (usually Thai and/or English).
Transcribe the page into clean Markdown:
- Keep the original language and wording exactly; do not translate, summarize, or correct the content.
- Use # / ## / ### for headings, - or 1. for lists, and Markdown tables for tables (merge split cells sensibly).
- Leave out running headers, footers, and page numbers.
- For a chart, photo, or diagram, write one short line in the page's language describing it, in italics.
- If the page is blank, reply with nothing.
Reply with the Markdown only, without a code fence or any comment."""


class ClaudeError(Exception):
    pass


def claude_client():
    global _claude
    if not ANTHROPIC_API_KEY:
        raise ClaudeError("ยังไม่ได้ตั้งค่า ANTHROPIC_API_KEY ใน pdf-worker")
    import anthropic

    if _claude is None:
        _claude = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY, max_retries=3)
    return _claude


def model_args(model: str) -> dict:
    """Request options for a model. Haiku has no server-side fallback, so it is only added elsewhere."""
    args = {"model": model, "thinking": {"type": "adaptive"}, "output_config": {"effort": CLAUDE_EFFORT}}
    if "haiku" not in model:
        args.update(betas=["server-side-fallback-2026-07-01"], fallbacks="default")
    return args


def claude_page(page: fitz.Page) -> str:
    import base64

    client = claude_client()
    # Keep the long side near 1568px, the size Claude reads at without downscaling.
    zoom = min(CLAUDE_DPI / 72, 1568 / max(page.rect.width, page.rect.height))
    pix = page.get_pixmap(matrix=fitz.Matrix(zoom, zoom))
    data = base64.standard_b64encode(pix.tobytes("png")).decode()
    resp = client.beta.messages.create(
        **model_args(CLAUDE_MODEL),
        max_tokens=16000,
        messages=[{"role": "user", "content": [
            {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": data}},
            {"type": "text", "text": CLAUDE_PROMPT},
        ]}],
    )
    if resp.stop_reason == "refusal":
        raise ClaudeError("Claude ปฏิเสธการอ่านหน้านี้")
    text = "".join(b.text for b in resp.content if b.type == "text").strip()
    text = re.sub(r"^```(?:markdown|md)?\s*\n|\n```\s*$", "", text)
    if resp.stop_reason == "max_tokens":
        text += "\n\n> หน้านี้ยาวเกินกำหนด เนื้อหาอาจไม่ครบ"
    return text


CLEAN_SYSTEM = """You receive a web page that was converted to Markdown automatically. It still contains the site's navigation menus, header, footer, breadcrumbs, sidebars, share buttons, cookie notices, and similar page furniture.
Return only the page's real content as Markdown, for a knowledge base:
- Copy the content's wording exactly. Do not translate, summarize, rephrase, or add anything that is not on the page.
- Keep the content's headings, lists, tables, dates, contact details, and the links that belong to the content.
- Remove menus, navigation links, language switchers, search boxes, login links, footers, "related" or "popular" link lists, social buttons, and repeated site-wide text.
- If the page is a listing (news, activities, documents), keep the list of items with their titles, dates, and links, and drop pagination.
- Start with the page's main heading as # if it has one.
- If the page has no real content beyond furniture, reply with nothing.
Reply with the Markdown only, without a code fence or any comment."""

MAX_CLEAN_CHARS = int(os.environ.get("MAX_CLEAN_CHARS", "150000"))


class CleanReq(BaseModel):
    markdown: str
    title: str = ""
    url: str = ""


@app.post("/clean", dependencies=[Depends(auth)])
def clean(req: CleanReq):
    """Let Claude keep only the real content of a converted web page."""
    try:
        client = claude_client()
    except ClaudeError as e:
        raise HTTPException(503, str(e)) from e
    md = req.markdown[:MAX_CLEAN_CHARS]
    page = f"URL: {req.url}\nTitle: {req.title}\n\n<page>\n{md}\n</page>"
    try:
        with client.beta.messages.stream(
            **model_args(CLAUDE_CLEAN_MODEL),
            max_tokens=32000,
            system=CLEAN_SYSTEM,
            messages=[{"role": "user", "content": page}],
        ) as stream:
            resp = stream.get_final_message()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"เรียก Claude ไม่สำเร็จ ({type(e).__name__})") from e
    if resp.stop_reason == "refusal":
        raise HTTPException(422, "Claude ปฏิเสธการคัดเนื้อหาหน้านี้")
    text = "".join(b.text for b in resp.content if b.type == "text").strip()
    text = re.sub(r"^```(?:markdown|md)?\s*\n|\n```\s*$", "", text)
    return {
        "markdown": text,
        "truncated": resp.stop_reason == "max_tokens" or len(req.markdown) > MAX_CLEAN_CHARS,
        "input_tokens": resp.usage.input_tokens,
        "output_tokens": resp.usage.output_tokens,
    }


def strip_repeating_lines(pages_md: list[str]) -> list[str]:
    """Remove headers/footers that repeat on many pages, and bare page numbers."""
    if len(pages_md) < 3:
        return [re.sub(r"(?m)^\s*(?:-\s*)?\d{1,4}(?:\s*-)?\s*$", "", p) for p in pages_md]
    edge = Counter()
    for md in pages_md:
        lines = [l.strip() for l in md.splitlines() if l.strip() and l.strip() != "-----"]
        for l in set(lines[:2] + lines[-2:]):
            # Headings and long sentences are content, never running headers.
            if len(l) <= 80 and not l.startswith("#"):
                edge[re.sub(r"\d+", "#", l)] += 1
    common = {k for k, v in edge.items() if v >= max(3, len(pages_md) * 0.5)}
    out = []
    for md in pages_md:
        kept = []
        for l in md.splitlines():
            key = re.sub(r"\d+", "#", l.strip())
            if key in common or re.fullmatch(r"\s*(?:-\s*)?\d{1,4}(?:\s*-)?\s*", l):
                continue
            kept.append(l)
        out.append("\n".join(kept))
    return out


@app.post("/files/{file_id}/convert", dependencies=[Depends(auth)])
def convert(file_id: str, req: ConvertReq):
    doc = open_pdf(path_for(file_id))
    pages = [p for p in req.pages if 1 <= p <= doc.page_count]
    if not pages:
        raise HTTPException(400, "ไม่มีหน้าที่อยู่ในไฟล์")
    use_claude = req.engine in ("claude", "claude_all")
    text_pages, scan_pages = [], []
    for p in pages:
        if req.engine == "claude_all":
            scan_pages.append(p)
            continue
        (text_pages if len(doc[p - 1].get_text("text").strip()) >= MIN_TEXT_CHARS else scan_pages).append(p)

    by_page: dict[int, str] = {}
    if text_pages:
        chunks = pymupdf4llm.to_markdown(doc, pages=[p - 1 for p in text_pages], page_chunks=True, show_progress=False)
        for c in chunks:
            n = int(c["metadata"].get("page", 0))
            by_page[n] = c["text"]
        # Fallback if metadata numbering differs between versions.
        if not set(by_page) & set(text_pages):
            by_page = {p: c["text"] for p, c in zip(text_pages, chunks)}
    ocr_done, claude_done = [], []
    for p in scan_pages:
        if use_claude:
            try:
                by_page[p] = claude_page(doc[p - 1])
                claude_done.append(p)
            except ClaudeError as e:
                by_page[p] = f"> อ่านหน้านี้ไม่สำเร็จ ({e})"
            except Exception as e:  # noqa: BLE001
                by_page[p] = f"> อ่านหน้านี้ไม่สำเร็จ (Claude: {type(e).__name__})"
        elif req.ocr:
            try:
                by_page[p] = ocr_page(doc[p - 1], req.lang)
                ocr_done.append(p)
            except Exception as e:  # noqa: BLE001
                by_page[p] = f"> อ่านหน้านี้ไม่สำเร็จ (OCR: {e})"
        else:
            by_page[p] = ""
    doc.close()

    ordered = [by_page.get(p, "") for p in pages]
    if req.strip_headers:
        ordered = strip_repeating_lines(ordered)
    parts = []
    for p, md in zip(pages, ordered):
        md = re.sub(r"(?m)^-----\s*$", "", md)
        md = re.sub(r"\n{3,}", "\n\n", md).strip()
        if not md:
            continue
        parts.append((f"[หน้า {p}]\n\n" if req.page_markers else "") + md)
    return {"markdown": "\n\n".join(parts), "ocr_pages": ocr_done, "claude_pages": claude_done}


@app.delete("/files/{file_id}", dependencies=[Depends(auth)])
def delete(file_id: str):
    path_for(file_id).unlink(missing_ok=True)
    (DATA_DIR / f"{file_id}.name").unlink(missing_ok=True)
    return {"ok": True}
