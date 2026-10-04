import { num, add } from "./mini";
import { z } from "zod";

function main(): number {
  return add(num(40), 2);
}
console.log(main());
