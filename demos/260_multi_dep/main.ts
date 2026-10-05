import { add } from "dep-a";
import { mulAdd } from "dep-b";
function main(): number {
  return add(1, 2) + mulAdd(3, 4, 5);
}
console.log(main());
