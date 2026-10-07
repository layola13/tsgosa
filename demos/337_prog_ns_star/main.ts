import * as B from "./lib/b";
function main(): number {
  return B.f() + B.g() * 10 + B.h() * 100;
}
console.log(main());
