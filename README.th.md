<h1 align="center">git-quill ✒️</h1>

<p align="center">
  <b>ให้ AI เขียน commit message, คำอธิบาย PR, changelog และรีวิวโค้ดเบื้องต้นจาก git diff ของคุณ</b><br>
  ไบนารีไฟล์เดียว · รันแบบ local ทั้งหมดได้ด้วย Ollama · ลบ secret ออกก่อนที่ข้อมูลจะออกจากเครื่อง
</p>

<p align="center">
  <a href="https://github.com/tonklasocute/git-quill/actions/workflows/ci.yml"><img src="https://github.com/tonklasocute/git-quill/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><a href="README.md">English</a></p>

<p align="center"><img src="docs/gifs/thai.gif" alt="สร้าง commit message ภาษาไทย" width="820"></p>

```console
$ git add -p
$ git quill commit --lang th   # เสนอข้อความ → [a]ccept [e]dit [r]egenerate [c]hoose [q]uit
$ git quill pr --create        # ชื่อ PR + คำอธิบาย แล้วเปิด PR ด้วย gh
$ git quill changelog --version v1.4.0 --write CHANGELOG.md
$ git quill review             # หาบั๊กที่น่าจะเกิด ปัญหาความปลอดภัย เทสต์ที่ขาด
```

## ทำไมต้อง git-quill

- **ไบนารีไฟล์เดียว** ไม่ต้องมี Node หรือ Python ติดตั้งด้วย `go install` หรือดาวน์โหลดจากหน้า Releases แล้วเรียก `git quill` ได้เลย (git จะรันโปรแกรมชื่อ `git-<ชื่อ>` ที่อยู่ใน `PATH` ให้อัตโนมัติ)
- **เป็นส่วนตัวตั้งแต่ต้น** secret ใน diff (เช่น cloud key, token, private key, รหัสผ่าน หรือค่าที่ดูสุ่มในไฟล์ `.env`) จะถูกแทนด้วย `[REDACTED:<ชนิด>]` *ก่อน*สร้าง request พร้อมแสดงคำเตือนว่าตัดอะไรออกไปบ้าง ส่วน lockfile, ไบนารี, โค้ดที่ generate และ vendor จะไม่ถูกส่งออกไปเลย และ `--dry-run --show-prompt` จะแสดงทุกอย่างที่จะถูกส่งโดยไม่ส่งจริง
- **รันในเครื่องทั้งหมดได้** รองรับ Ollama และ LM Studio เต็มรูปแบบ ฟรี ใช้ offline ได้ ไม่ต้องสมัครบัญชี
- **รับมือ diff ใหญ่ได้** ถ้าเกิน token budget จะตัดไฟล์ที่ไม่จำเป็นออก ลดบรรทัด context แล้วสรุปทีละไฟล์ (map-reduce) และบอกทุกครั้งว่าใช้วิธีไหน
- **ไทยและอังกฤษ** ใช้ `--lang th` จะได้ภาษาไทยที่อ่านเป็นธรรมชาติ โดย type, scope และชื่อในโค้ดยังเป็นภาษาอังกฤษ prompt ทั้งสองภาษามี golden test คุมไว้
- **ใช้ provider ไหนก็ได้** Anthropic, OpenAI, Google Gemini, Ollama, OpenRouter, Groq, LM Studio หรือ endpoint ใดก็ได้ที่ compatible กับ OpenAI และกำหนด model id เองได้ทุกตัว
- **ทำได้มากกว่า commit** มีทั้งคำอธิบาย PR, release notes แบบ Keep a Changelog, รีวิวที่อ้างอิงเลขบรรทัด และ hook `prepare-commit-msg` สำหรับคนที่ใช้ `git commit` ธรรมดา

## ติดตั้ง

```sh
go install github.com/tonklasocute/git-quill/cmd/git-quill@latest
```

หรือดาวน์โหลดไบนารี (Linux, macOS, Windows ทั้ง amd64 และ arm64) จาก [Releases](https://github.com/tonklasocute/git-quill/releases) แล้วตรวจสอบกับไฟล์ `checksums.txt` ส่วน Homebrew tap ยังไม่เปิด (ระหว่างนี้ใช้ `go install` หรือไบนารีจาก Releases)

## ตั้งค่าใน 60 วินาที

```sh
git quill config        # wizard: เลือก provider, model, ภาษา และเก็บ API key ไว้ใน keychain ของระบบ
```

**Ollama (ฟรี ข้อมูลไม่ออกจากเครื่อง แนะนำถ้าห่วงเรื่องความเป็นส่วนตัว)**

```sh
# ติดตั้งจาก https://ollama.com/download แล้วรัน
ollama pull qwen3.5:4b
git quill config set provider ollama
git quill config set lang th
git quill commit
```

**Anthropic (Claude) ซึ่งเป็นค่าเริ่มต้น**

```sh
export ANTHROPIC_API_KEY=sk-ant-...    # หรือ git quill config set-key anthropic
git quill commit                       # model เริ่มต้น: claude-haiku-5-5
```

**OpenAI / Gemini / OpenRouter / Groq / LM Studio** ใช้วิธีเดียวกัน คือตั้ง key ผ่าน env (`OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`) หรือ `config set-key` จากนั้นใช้ `git quill config set provider <ชื่อ>` (ดูรายละเอียดใน [README ภาษาอังกฤษ](README.md#60-second-setup))

## คำสั่ง

| คำสั่ง | ทำอะไร |
|---|---|
| `git quill commit` | อ่าน diff ที่ staged แล้วเสนอ Conventional Commit ใน terminal จะมี TUI ให้ accept/edit/regenerate/choose/quit จากนั้นจึงรัน `git commit` |
| `git quill pr` | ชื่อ PR และคำอธิบาย (Summary, Changes, Testing, Breaking changes) จาก commit และ diff ของ branch เทียบกับ main/master หรือ `--base` ส่วน `--create` จะเปิด PR ผ่าน `gh` โดยถามก่อน |
| `git quill changelog` | release notes แบบ Keep a Changelog จาก commit ตั้งแต่ tag ล่าสุด (หรือ `--from/--to`) และ `--write CHANGELOG.md` จะแทรกส่วนใหม่ไว้ด้านบน |
| `git quill review` | รีวิว diff ที่ staged (หรือทั้ง branch ด้วย `--branch`) แยกตามไฟล์ มีเลขบรรทัดและระดับความรุนแรง ทั้งหมดเป็นแค่ข้อเสนอแนะของ AI ปกติคืนค่า exit 0 เสมอ ยกเว้นใส่ `--strict` |
| `git quill hook install` | ติดตั้ง hook `prepare-commit-msg` ให้ `git commit` ธรรมดาได้ข้อความที่เติมไว้ให้แล้ว โดยข้าม merge, squash, amend และ `-m` และไม่ทำให้ commit ล้มเหลวเด็ดขาด |
| `git quill config` | setup แบบ interactive รวมถึง `get`/`set`/`list`/`set-key`/`delete-key`/`path` |

flag ที่ใช้บ่อยของ `commit`:

- `-n 3` สร้างข้อความ 3 แบบให้เลือก
- `--yes` commit เลยโดยไม่ถาม
- `--dry-run` แสดงข้อความอย่างเดียว ไม่ commit
- `--show-prompt` แสดง request ที่จะส่ง
- `--lang th|en` เลือกภาษา
- `--type fix` บังคับ type
- `--context "..."` ใส่บริบทเพิ่มให้ model
- `--no-body` เอาแค่บรรทัด subject
- `--emoji` ใส่ gitmoji นำหน้า
- `-v` แสดง token ที่ใช้ไป

## ข้อมูลอะไรบ้างที่ถูกส่งให้ AI

**ส่ง:**

- คำสั่งของระบบ (system prompt)
- ชื่อ branch
- `--context` ที่คุณพิมพ์ (ผ่านการ redact แล้ว)
- diff ที่ผ่านการ redact แล้ว (เฉพาะบรรทัดที่เปลี่ยนกับ context ไม่เกิน 3 บรรทัด)
- *ชื่อ* ไฟล์ที่ถูกตัดออก (ไม่ส่งเนื้อหา)
- สำหรับ `pr`/`changelog` จะส่งข้อความ commit ที่ redact แล้วไปด้วย

**ไม่ส่ง:** เนื้อหาของไฟล์ที่ถูกตัดออก, ไฟล์ที่คุณไม่ได้แก้, git config และ API key ของ provider อื่น

**ตัดออกเสมอ:** lockfile, `vendor/`, `node_modules/`, ไฟล์ minified/source map, โค้ดที่ generate, รูปภาพ, สื่อ, ฟอนต์, archive, ไบนารี และไฟล์ฐานข้อมูล (ถ้าอยากให้รวมไฟล์ไหนกลับมา ให้ใส่ `!pattern` ใน `.git-quill-ignore`)

**Redact:** AWS key, token ของ GitHub/GitLab/Slack, key ของ Anthropic/OpenAI/Google/Stripe/npm, private key (รวมแบบหลายบรรทัด), JWT, `Bearer`/`Basic`, รหัสผ่านใน URL, การกำหนดค่าแบบ `password=`/`secret:`/`api_key=` และค่าที่ดูสุ่มในไฟล์ `.env`

ตรวจสอบเองได้ด้วย `git quill commit --dry-run --show-prompt` ซึ่งจะแสดง request แบบเต็มโดยไม่ส่งอะไรออกไปเลย การ redact เป็น heuristic ช่วยลดความเสี่ยงได้แต่ไม่รับประกัน 100% จึงควรใช้คู่กับ secret scanner เช่น gitleaks ถ้าไม่ต้องการให้ข้อมูลออกจากเครื่องเลย ให้ใช้ **Ollama** หรือ **LM Studio**

<img src="docs/gifs/redaction.gif" alt="secret ถูก redact ก่อนส่ง" width="760">

## การตั้งค่า

ลำดับความสำคัญจากต่ำไปสูง:

1. ค่าเริ่มต้น
2. `~/.config/git-quill/config.toml`
3. `.git-quill.toml` ใน repo
4. env (`GIT_QUILL_PROVIDER`, `GIT_QUILL_MODEL`, `GIT_QUILL_LANG`)
5. flag ของคำสั่ง

> **กฎความปลอดภัย:** `.git-quill.toml` ใน repo ตั้ง `provider` หรือ `base_url` ไม่ได้ เพื่อกันไม่ให้ repo ที่ clone มาแอบส่งโค้ดและ API key ของคุณไปที่อื่น

API key อ่านจาก env หรือ keychain ของระบบเท่านั้น (macOS Keychain, Windows Credential Manager, Secret Service บน Linux) **ไม่เคยถูกเขียนลงไฟล์ config** ดูตาราง key ทั้งหมดได้ใน [README ภาษาอังกฤษ](README.md#configuration)

## Model เริ่มต้น (ตรวจสอบเมื่อ 2026-10-19)

| Provider | Model เริ่มต้น | ตัวที่ถูก/เร็วกว่า |
|---|---|---|
| Anthropic | `claude-haiku-5-5` | (ตัวใหญ่กว่า: `claude-sonnet-5-5`, `claude-opus-5-5`) |
| OpenAI | `gpt-6.1-sol` | `gpt-6-luna` |
| Gemini | `gemini-3.8-flash` | `gemini-3.5-flash-lite` |
| Ollama | `qwen3.5:4b` | `gemma4:e4b` |

เปลี่ยนได้ทุกตัวด้วย `git quill config set model <id>`

**ทดสอบกับของจริงแล้ว** (2026-10-19) ด้วย Ollama 0.40.6 และ `qwen3.5:4b` ที่รันบน CPU (ผ่าน Docker บน Mac) ทุกคำสั่งทำงานครบตั้งแต่ต้นจนจบ เวลาที่ใช้: commit 10–25 วินาที, review ประมาณ 45 วินาที, pr ประมาณ 50 วินาที (ถ้ามี GPU จะเร็วกว่านี้มาก) ส่วน provider บน cloud ทดสอบกับ mock server ที่จำลอง API จริงเท่านั้น ยังไม่ได้เรียกใช้บริการจริง

## คำถามที่พบบ่อย

**ทำไมชื่อ git-quill ไม่ใช่ git-ai?** เพราะชื่อ `git-ai` มี [git extension ที่ยัง active อยู่](https://github.com/git-ai-project/git-ai) ใช้อยู่แล้ว ถ้าใช้ชื่อเดียวกัน binary สองตัวจะชนกันใน `PATH`

**ภาษาไทยคุณภาพเป็นอย่างไร?** ขึ้นกับ model ที่ใช้ model ใหญ่จะเขียนไทยได้เป็นธรรมชาติ ส่วน model เล็กที่รันในเครื่องใช้งานได้แต่จะเรียบๆ กว่า ทั้งนี้ type, scope และชื่อไฟล์ยังเป็นภาษาอังกฤษเสมอ เครื่องมือที่ parse Conventional Commits จึงยังทำงานได้ตามปกติ

**model เล็กที่รันในเครื่องใช้ได้ดีแค่ไหน?** จากการทดสอบกับ `qwen3.5:4b` commit message ถูกต้อง และภาษาไทยอ่านเป็นธรรมชาติ แต่มีข้อสังเกตบางอย่าง:

- มักเลือก `feat` ทั้งที่จริงเป็นการแก้ bug (แก้ได้ด้วย `--type fix`)
- บางครั้งให้ตัวเลือกน้อยกว่าที่ขอด้วย `-n`
- ให้ระดับความรุนแรงใน review สูงเกินจริง
- บางครั้งเขียนคำอธิบาย PR ไม่จบ (git-quill จะเตือนเมื่อหัวข้อไม่ครบ)

model ที่ใหญ่กว่าไม่มีปัญหาเหล่านี้

**ใช้ใน CI ได้ไหม?** ได้ ใช้ `git quill commit --yes` หรือ `--dry-run` และ `git quill review --strict` จะทำให้ job ล้มเหลวเมื่อเจอปัญหาระดับ high/medium

**macOS แจ้งว่าเปิดไบนารีที่ดาวน์โหลดมาไม่ได้?** ไบนารีใน Releases ยังไม่ได้ sign ไว้ ไฟล์ที่ดาวน์โหลดผ่าน browser จึงถูก Gatekeeper กักไว้ แก้ได้ด้วย `xattr -d com.apple.quarantine ./git-quill` หรือติดตั้งด้วย `go install` แทน

**ข้าม hook ครั้งเดียวทำอย่างไร?** ใช้ `GIT_QUILL_SKIP_HOOK=1 git commit` หรือใส่ `-m` ไปเลย

## Contributing และ License

ดู [CONTRIBUTING.md](CONTRIBUTING.md) · [MIT](LICENSE) © 2026 Khomkrit Daengnuan
