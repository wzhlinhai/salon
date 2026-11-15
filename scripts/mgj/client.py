"""后端 HTTP API 的最小客户端：登录、业务操作和导入预览。"""

import json
import uuid
import urllib.error
import urllib.request
from pathlib import Path


class ApiError(RuntimeError):
    """后端返回非 2xx 时保留状态码和 detail。"""

    def __init__(self, status, detail):
        super().__init__(f"HTTP {status}: {detail}")
        self.status = status
        self.detail = detail


def _detail(raw):
    try:
        payload = json.loads(raw)
    except (ValueError, TypeError):
        return raw.decode("utf-8", "replace")[:500]
    if isinstance(payload, dict) and "detail" in payload:
        detail = str(payload["detail"])
        errors = payload.get("errors")
        if isinstance(errors, list):
            messages = [
                str(error.get("message"))
                for error in errors
                if isinstance(error, dict) and error.get("message")
            ]
            if messages:
                detail += ": " + "; ".join(messages)
        return detail
    return json.dumps(payload, ensure_ascii=False)[:500]


class Client:
    """保存登录凭证，并封装 /api/auth、/api/ops 和 /api/import。"""

    def __init__(self, base_url):
        self.base = base_url.rstrip("/")
        self.token = None

    def _request(self, method, path, data=None, headers=None, timeout=60):
        request_headers = {"Accept": "application/json"}
        if self.token:
            request_headers["Authorization"] = "Bearer " + self.token
        if headers:
            request_headers.update(headers)
        request = urllib.request.Request(
            self.base + path, data=data, headers=request_headers, method=method
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                raw = response.read()
        except urllib.error.HTTPError as error:
            raise ApiError(error.code, _detail(error.read())) from error
        except urllib.error.URLError as error:
            raise ApiError(0, str(error.reason)) from error
        if not raw:
            return {}
        return json.loads(raw)

    def _post_json(self, path, payload):
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        return self._request(
            "POST", path, data=data, headers={"Content-Type": "application/json"}
        )

    def login(self, account, password):
        """登录并保存服务端返回的 Bearer 凭证。"""
        response = self._post_json(
            "/api/auth/login", {"account": account, "password": password}
        )
        self.token = response["token"]
        return response

    def op(self, name, payload=None):
        """调用 POST /api/ops/<name>。"""
        return self._post_json("/api/ops/" + name, payload or {})

    def preview(self, xlsx_path):
        """以 multipart 字段 file 上传中间 Excel。"""
        path = Path(xlsx_path)
        boundary = "----mgj" + uuid.uuid4().hex
        content = path.read_bytes()
        head = (
            f"--{boundary}\r\n"
            f'Content-Disposition: form-data; name="file"; filename="{path.name}"\r\n'
            "Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet\r\n\r\n"
        ).encode("utf-8")
        tail = f"\r\n--{boundary}--\r\n".encode("utf-8")
        return self._request(
            "POST",
            "/api/import/preview",
            data=head + content + tail,
            headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        )
