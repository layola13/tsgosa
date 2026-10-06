import { createHash } from "crypto";
function main(): i32 {
  const h = createHash("sha256");
  h.update("abc");
  h.update("def");
  const d: string = h.digest("hex");
  const g = createHash("sha256");
  g.update("abc");
  const x: string = g.digest("hex");
  return d.length + x.length;
}
console.log(main());
