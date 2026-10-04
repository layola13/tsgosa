import { V } from "./v";
import wrap from "./v";
function main(): i32 {
  const c = new V.Strict(1, 10);
  const a = c.check(5);
  const b = V.clamp(99, 1, 10);
  const d = wrap.inRange(5, 1, 10);
  return a * 100 + b + d;
}
console.log(main());
