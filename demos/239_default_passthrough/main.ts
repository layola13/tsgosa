import * as lib from "./lib";
import D from "./mid";
function main(): i32 {
  return lib.add(1, 2) + D.add(3, 4);
}
console.log(main());
