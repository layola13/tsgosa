import { assertTrue, assertEq, assertNe, assertStrEq } from "./assert";
import { add, mul, greet } from "./math";
export function verifyAddition(): void {
  assertEq(add(-10, 5), -5);
  assertNe(add(1, 1), 3);
  assertTrue(1);
}
export function verifyMul(): void {
  assertEq(mul(3, 4), 12);
  assertEq(mul(0, 99), 0);
}
export function verifyGreet(): void {
  assertStrEq(greet("sa"), "hi sa");
}
export function verifyCatchRecovery(): void {
  let y = 0;
  try {
    y = 5;
    throw 42;
  } catch (e) {
    y = e;
  }
  assertEq(y, 42);
}
export function verifyCatchSkipped(): void {
  let y = 0;
  try {
    y = 1;
  } catch (e) {
    throw e;
  }
  assertEq(y, 1);
}
function main(): number {
  let passed = 0;
  verifyAddition();
  passed = passed + 1;
  verifyMul();
  passed = passed + 1;
  verifyGreet();
  passed = passed + 1;
  verifyCatchRecovery();
  passed = passed + 1;
  verifyCatchSkipped();
  passed = passed + 1;
  return passed;
}
console.log(main());
