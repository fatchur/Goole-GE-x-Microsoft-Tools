# cngpt-frontend

Frontend demo **sesederhana mungkin** untuk latihan pola BFF SSO — satu
file HTML memakai [Vue 3](https://vuejs.org) lewat CDN, tanpa build tool,
tanpa `npm install`.

Ini proyek **terpisah** dari backend (`cngpt-bff-sso/`). Keduanya
dijalankan sebagai dua server berbeda, di dua terminal berbeda, dan cuma
saling "ngobrol" lewat HTTP request (fetch, redirect) — bukan satu proses
yang sama.

## Isi

```
cngpt-frontend/
└── index.html   <- seluruh HTML, CSS, dan JS (Vue) ada di satu file ini
```

## ⚠️ Jangan dibuka langsung dengan double-click

Kalau file ini dibuka langsung dari File Explorer/Finder (jadi URL-nya
`file:///.../index.html`), browser akan memberi origin `null` ke halaman
itu — cookie session dari backend **tidak akan tersimpan/terkirim dengan
benar**, dan CORS akan menolak permintaan. Harus disajikan lewat HTTP
server, walau sesederhana apa pun servernya.

## Cara menjalankan

Pastikan backend (`cngpt-bff-sso/`) sudah jalan lebih dulu di
`http://localhost:8080` (lihat README di proyek itu).

Lalu jalankan salah satu:

**Opsi A — pakai Python (biasanya sudah terpasang):**
```bash
python3 -m http.server 5173
```

**Opsi B — pakai Node:**
```bash
npx serve -l 5173
```

Buka browser ke `http://localhost:5173`.

## Kenapa harus port 5173

Backend punya konfigurasi `FRONTEND_URL` (default
`http://localhost:5173`) yang dipakai untuk:
1. Redirect tujuan setelah login Microsoft sukses
2. Header CORS `Access-Control-Allow-Origin`

Kalau frontend ini di-serve di port lain, ubah juga `FRONTEND_URL` di
`.env` backend supaya tetap cocok satu sama lain.

## Mengubah alamat backend

Kalau backend dijalankan di alamat/port lain, cukup ubah satu baris ini
di `index.html`:

```javascript
const BACKEND_URL = "http://localhost:8080";
```

## Apa yang dilakukan halaman ini

| Aksi user | Yang terjadi |
|---|---|
| Buka halaman | Panggil `GET /api/me` untuk cek status login |
| Klik "Login dengan Microsoft" | `window.location.href` ke `/auth/login` (navigasi biasa, **bukan** `fetch`, karena akan redirect ke domain Microsoft) |
| Ketik pesan & kirim | `POST /api/chat`, tampilkan balasan sebagai chat bubble |
| Klik "Logout" | `POST /auth/logout`, kembali ke halaman login |

Semua request ke backend memakai `credentials: "include"` supaya cookie
session ikut terkirim — frontend ini sendiri **tidak pernah** menyimpan
atau memegang token Microsoft/Google apa pun, sesuai pola BFF yang
dijelaskan di dokumentasi arsitektur (Bagian 6).
