import { add } from "js-cjs";
import { greet } from "js-esm";
import { mul } from "js-mjs";
import twice from "js-cjsdef";

export function main(): i32 {
  console.log(add(40, 2));
  console.log(greet());
  console.log(mul(6, 7));
  console.log(twice(40, 2));
  return 0;
}
