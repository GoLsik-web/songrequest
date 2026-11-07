"""Принимает результат export.html (кадры и звук) и сохраняет в файл.

Запуск: python -I receive.py <файл>; потом открыть export.html в Chrome.
"""
import http.server
import sys

OUT = sys.argv[1]


class H(http.server.BaseHTTPRequestHandler):
    def do_OPTIONS(self):
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()

    def do_POST(self):
        n = int(self.headers["Content-Length"])
        open(OUT, "wb").write(self.rfile.read(n))
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        raise SystemExit


http.server.HTTPServer(("127.0.0.1", 8765), H).handle_request()
