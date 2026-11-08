const express = require("express");
const path = require("node:path");

const app = express();
const port = Number(process.env.PORT || 8080);

app.get("/healthz", (_req, res) => res.send("ok"));
app.get("/api/info", (_req, res) => {
  res.json({
    version: process.env.APP_VERSION,
    hasSessionSecret: Boolean(process.env.SESSION_SECRET),
  });
});
app.use(express.static(path.join(__dirname, "dist")));

app.listen(port, "0.0.0.0");
