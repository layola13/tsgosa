import { C } from "./mid";
function main(): number {
  const d = new C(3, 4);
  return d.x + d.y;
}
console.log(main());
