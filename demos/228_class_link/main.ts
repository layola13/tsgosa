import { add } from "./util";
import { Box } from "./types";

function main(): number {
  const b = new Box();
  b.set(add(20, 20));
  return b.v;
}
console.log(main());
