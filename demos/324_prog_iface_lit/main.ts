import { get } from "./lib/b";
export function main(): i32 {
  const r = get({ v: 5 });
  console.log(r);
  return r;
}
