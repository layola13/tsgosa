import { add } from "js-cjs";
import { greet } from "js-esm";
import { mul } from "js-mjs";

export function main(): i32 {
  console.log(add(40, 2));
  console.log(greet());
  console.log(mul(6, 7));
  return 0;
}
