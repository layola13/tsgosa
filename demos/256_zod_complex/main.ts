import { computeZod, Schema } from "./schemas";
function main(): number {
  const sc = new Schema(7, "hi");
  const cross = sc.check("hi") + sc.check("bye");
  return computeZod() + cross * 100000;
}
console.log(main());
