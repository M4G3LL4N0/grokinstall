import http from "node:http";

http.createServer((req, res) => res.end("ok")).listen(3000);
