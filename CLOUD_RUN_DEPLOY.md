# Deploy `dupaybe` ke GCP Cloud Run

## 1) Build image dari folder ini

```bash
gcloud builds submit --tag gcr.io/<PROJECT_ID>/dupaybe
```

## 2) Deploy ke Cloud Run

```bash
gcloud run deploy dupaybe \
  --image gcr.io/<PROJECT_ID>/dupaybe \
  --platform managed \
  --region <REGION> \
  --allow-unauthenticated \
  --set-env-vars APP_ENCRYPTION_KEY=<APP_ENCRYPTION_KEY> \
  --set-env-vars DATABASE_URL=<DATABASE_URL>
```

## 3) Catatan

- Aplikasi otomatis listen ke `PORT` dari Cloud Run (fallback `8080`).
- Jangan simpan kredensial di `.env` saat production.
- Disarankan pakai Secret Manager untuk `DATABASE_URL` dan `APP_ENCRYPTION_KEY`.
