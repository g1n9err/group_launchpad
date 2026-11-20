# Group Launchpad — արտադրական տեղակայման ուղեցույց (DEPLOY.md)

Կարճ, գործնական քայլեր՝ մարդ-օպերատորի համար։ Բոլոր գաղտնիքները միայն
`deploy/.env.production`-ում են (երբեք commit չի արվում)։

## 0. Նախապայմաններ

- Սերվեր՝ Docker + Docker Compose v2-ով
- Domain՝ DNS A գրառումով դեպի սերվեր
- Ռեպոն՝ `main` ճյուղով (CI-ն կանաչ է)
- (Ըստ ցանկության) Slack app + Google Cloud OAuth client՝ PRD-ի ինտեգրումների համար

## 1. Ֆայլերը և environment-ը

```bash
git clone git@github.com:g1n9err/group_launchpad.git && cd group_launchpad
cp deploy/env.production.example deploy/.env.production
# լրացրու՝ POSTGRES_PASSWORD, COORDINATOR_PASSWORD, TOKEN_ENCRYPTION_KEY,
# CORS_ALLOWED_ORIGINS (SPA-ի https հասցեն), և մեկ անգամյա ADMIN_EMAIL/ADMIN_INITIAL_PASSWORD
```

## 2. Բազայի կրկնօրինակ (միգրացիայից առաջ միշտ)

```bash
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production up -d postgres
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production \
  exec postgres pg_dump -U launchpad -Fc launchpad > backup-$(date +%F).dump
```

## 3. Մեկնարկ (միգրացիաները կիրառվում են բուտի ժամանակ՝ մինչև v16)

```bash
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production \
  --profile full up -d --build
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production \
  exec api wget -qO- http://localhost:8080/readyz   # {"status":"ready"}
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production \
  exec postgres psql -U launchpad -d launchpad -tAc "SELECT version FROM schema_migrations;"  # 16
```

Այլընտրանք՝ միգրացիան առանձին, բուտից առաջ (պահանջում է Go toolchain կամ
`backend/cmd/migrate`-ի docker build)։

## 4. Առաջին admin + գաղտնաբառի env-ների հեռացում

1. Բրաուզերով բացիր `https://<domain>/` → «Մուտք գործել» → `ADMIN_EMAIL` +
   `ADMIN_INITIAL_PASSWORD` (bootstrap-ը ստեղծում է admin-ը միայն եթե ոչ մի admin չկա)։
2. Նույն սեսիայով ստեղծիր քո մշտական հաշիվները (օր. coordinator)` 
   `POST /api/admin/users` (Account → կառավարում, կամ curl)։
3. **Delete** `ADMIN_EMAIL`/`ADMIN_INITIAL_PASSWORD` տողերը `.env.production`-ից և
   `docker compose ... --profile full up -d` → api կոնտեյները վերաստեղծվում է առանց դրանց։

## 5. HTTPS (reverse proxy)

API-ն լսում է միայն `127.0.0.1:8080`։ Caddy-ի նվազագույն օրինակ (ավտոմատ TLS).

```
launchpad.example.am {
    encode zstd gzip
    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }
    handle {
        root * /srv/launchpad-spa     # frontend/dist-ի պարունակությունը
        try_files {path} /index.html
        file_server
    }
}
```

SPA-ն կառուցում ես այսպես (VITE_API_URL-ը public API հասցեն է)`

```bash
cd frontend && VITE_API_URL=https://launchpad.example.am npm run build
# dist/ → /srv/launchpad-spa
```

Եթե պրոքսին **վերագրահրում** է `X-Forwarded-For`-ը → `.env.production`-ում
`TRUST_PROXY=true` (login-throttle-ի IP-հաշվարկի ճշգրտության համար)։ Slack Events
listener-ը (8081) բաց արա միայն թունելով/proxy-ով՝ signing secret-ով պաշտպանված։

## 6. Ինտեգրումներ (երբ որոշես միացնել)

- Slack. `SLACK_BOT_TOKEN` + `SLACK_SIGNING_SECRET` (Slack app-ի կարգավորումներից)։
- Google. `GOOGLE_CLIENT_ID/SECRET`, `GOOGLE_REDIRECT_URL`
  (`https://<domain>/api/integrations/google/callback`), `TOKEN_ENCRYPTION_KEY`։
- Լրացնելուց հետո` `--profile full up -d` → Integration էջից Google Connect։

## 7. Ծխափորձ տեղակայումից հետո

- `/health`, `/readyz` → 200
- Մուտք էկրանից, խմբի ստեղծում, launch (Slack/Google-ը միացված են՝ իրական
  ռեսուրսներ կստեղծվեն. նախ տվյալները ստուգիր), notification-ներ, մեկ աուդիտ
- Worker-ի լոգերում՝ «river worker started»

## 8. Rollback

```bash
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.production \
  --profile full down            # կանգնեցնել
docker compose ... exec postgres pg_restore -U launchpad -d launchpad --clean backup-<date>.dump
git checkout <նախորդ tag/main commit> && docker compose ... up -d --build
```

Միգրացիաների հետքայլ՝ `cd backend && go run ./cmd/migrate down 1` (փուլ առ փուլ,
0016-ի down-ը կջնջի միայն auth աղյուսակները)։

## Հայտնի սահմանափակումներ

- Login-throttle-ը **per-process** է. մեկ API instance-ի համար նախատեսված է
  (PRD-ի single-instance մոդել). մի քանի replica-ի դեպքում հաշվիչը տեղափոխի ընդհանուր բազա։
- Backups. pg_dump cron + կրկնօրինակները մեկ այլ մեքենայում (օպերատորի պատասխանատվություն)։
