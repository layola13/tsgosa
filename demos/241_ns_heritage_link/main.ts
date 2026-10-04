import { N } from "./lib";
function main(): i32 {
  const d = new N.D(3, 4);
  return d.x + d.y;
}
console.log(main());
