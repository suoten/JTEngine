const fs = require("fs");
process.argv.slice(2).forEach(f => {
  const sep = f.indexOf(":::");
  const path = f.substring(0, sep);
  const b64 = f.substring(sep + 3);
  fs.writeFileSync(path, Buffer.from(b64, "base64").toString("utf8"), "utf8");
  console.log("OK: " + path);
});
