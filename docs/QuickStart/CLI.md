# 💻 Quick Start — CLI
This guide shows how to test **backend-antiginx** directly from your terminal using `curl`.


<br>


## ✅ Requirements
- Running **backend-antiginx** API (locally or in Docker)
- PostgreSQL and RabbitMQ reachable by backend
- Go 1.25+ (for local run without Docker)
- `curl`
- Optional: `jq` (for pretty JSON output and token extraction)


<br>


## ⚡ Quick Start
Start backend first (so `curl` calls can work):
```bash
# from repository root
cp .env.example .env
```

Then edit `.env` and ensure these values are set:
```dotenv
DATABASE_URL=postgres://user:password@localhost:5432/antiginx
RABBITMQ_URL=amqp://user:password@localhost:5672/
JWT_SECRET=super-secret-key
```

Run the API:
```bash
go run main.go
```

Set API base URL:
```bash
export BASE_URL="http://localhost:4000/api"
```

Quick health check:
```bash
curl -s ${BASE_URL}/health
```

✅ **Expect:** `{"message":"Running..."}`


<br>


## 📖 Available API Flow
| Step | Endpoint | Purpose |
|---|---|---|
| `register` | `POST /auth/register` | Create account |
| `login` | `POST /auth/login` | Get JWT token |
| `me` | `GET /auth/me` | Validate JWT and fetch current user |
| `submit scan` | `POST /scans` | Create scan task (`PENDING`) |
| `get scan` | `GET /scans/{id}` | Read scan details and results |
| `submit result` | `POST /results` | Worker-style result callback |

**📌 Important Notes:**

- Password for register/login must have at least 8 characters.
- `testId` in `/results` should be the same UUID returned as `scanId` from `/scans`.
- `jq` is optional; remove `| jq` from commands if not installed.


<br>


## 1️⃣ Register User
```bash
curl -s -X POST ${BASE_URL}/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "full_name":"Jane Doe",
    "email":"jane@example.com",
    "password":"SecurePass123"
  }'
```
✅ **Expect:** `{"message":"User registered successfully"}`


<br>


## 2️⃣ Login & Save Token
```bash
TOKEN=$(curl -s -X POST ${BASE_URL}/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email":"jane@example.com",
    "password":"SecurePass123"
  }' | jq -r '.token')

echo "Token: ${TOKEN}"
```
✅ **Expect:** JSON with `token` and `expires_in`


<br>


## 3️⃣ Validate Auth (`/auth/me`)
```bash
curl -s ${BASE_URL}/auth/me \
  -H "Authorization: Bearer ${TOKEN}" | jq
```
✅ **Expect:** user `id`, `full_name`, `email`


<br>


## 4️⃣ Submit a Scan
```bash
SCAN_ID=$(curl -s -X POST ${BASE_URL}/scans \
  -H "Content-Type: application/json" \
  -d '{"target_url":"https://example.com"}' | jq -r '.scanId')

echo "Scan ID: ${SCAN_ID}"
```
✅ **Expect:** `scanId` and `status` (`PENDING`)


<br>


## 5️⃣ Retrieve Scan
```bash
curl -s ${BASE_URL}/scans/${SCAN_ID} | jq
```
✅ **Expect:** scan metadata + results array


<br>


## 6️⃣ Submit Worker Result (Manual Simulation)
Normally sent by worker, but you can test manually:
```bash
curl -s -X POST ${BASE_URL}/results \
  -H "Content-Type: application/json" \
  -d '{
    "target":"https://example.com",
    "testId":"'"${SCAN_ID}"'",
    "result":{
      "Name":"csp-header",
      "Certainty":90,
      "ThreatLevel":"Info",
      "Description":"CSP header present but missing frame-ancestors directive",
      "Metadata":{"header":"Content-Security-Policy"}
    }
  }' | jq
```
✅ **Expect:** `{"message":"Result received"}`


<br>


## 7️⃣ Mark Scan as Completed (Optional)
In this backend, sending an empty `result.Name` marks scan as completed:
```bash
curl -s -X POST ${BASE_URL}/results \
  -H "Content-Type: application/json" \
  -d '{
    "target":"https://example.com",
    "testId":"'"${SCAN_ID}"'",
    "result":{
      "Name":"",
      "Certainty":0,
      "ThreatLevel":"Info",
      "Description":"",
      "Metadata":{}
    }
  }' | jq
```
✅ **Expect:** `{"message":"Scan completed"}`


<br>


## 🧾 Status Codes & Common Headers
| Status | Meaning | Action |
| --- | --- | --- |
| `200 OK` | Request successful | Continue flow |
| `202 Accepted` | Scan accepted and queued | Poll with `GET /scans/{id}` |
| `400 Bad Request` | Invalid payload/UUID | Verify JSON and `testId` format |
| `401 Unauthorized` | Missing/invalid token | Login again and resend header |
| `404 Not Found` | Scan does not exist | Check `scanId` value |
| `500 Internal Server Error` | Backend dependency/runtime issue | Check backend logs |


<br>


| Header | Usage | Required For |
| --- | --- | --- |
| `Authorization: Bearer <token>` | JWT authentication | `/auth/me` |
| `Content-Type: application/json` | JSON body | All POST requests |


<br>


## 🔁 Full Test Workflow
```bash
# 0) Base URL
export BASE_URL="http://localhost:4000/api"

# 1) Health
curl -s ${BASE_URL}/health | jq

# 2) Register
curl -s -X POST ${BASE_URL}/auth/register \
  -H "Content-Type: application/json" \
  -d '{"full_name":"Test User","email":"test@test.com","password":"TestPass123"}'

# 3) Login
TOKEN=$(curl -s -X POST ${BASE_URL}/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@test.com","password":"TestPass123"}' | jq -r '.token')

# 4) Verify token
curl -s ${BASE_URL}/auth/me -H "Authorization: Bearer ${TOKEN}" | jq

# 5) Submit scan
SCAN_ID=$(curl -s -X POST ${BASE_URL}/scans \
  -H "Content-Type: application/json" \
  -d '{"target_url":"https://example.com"}' | jq -r '.scanId')

# 6) Submit one result item
curl -s -X POST ${BASE_URL}/results \
  -H "Content-Type: application/json" \
  -d '{"target":"https://example.com","testId":"'"${SCAN_ID}"'","result":{"Name":"https","Certainty":90,"ThreatLevel":"Info","Description":"HTTPS check","Metadata":{}}}' | jq

# 7) Mark completed
curl -s -X POST ${BASE_URL}/results \
  -H "Content-Type: application/json" \
  -d '{"target":"https://example.com","testId":"'"${SCAN_ID}"'","result":{"Name":"","Certainty":0,"ThreatLevel":"Info","Description":"","Metadata":{}}}' | jq

# 8) Get final scan
curl -s ${BASE_URL}/scans/${SCAN_ID} | jq
```


<br>


## 🧹 Cleanup
```bash
unset TOKEN SCAN_ID BASE_URL
```