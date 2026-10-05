export function assertEq(actual: number, expected: number): void {
  if (actual != expected) {
    throw new Error("no");
  }
}
test("skipped would fail", () => {
  assertEq(1, 2);
});
test.only("focused runs", () => {
  assertEq(3, 3);
});
describe("also skipped", () => {
  test("nested would fail", () => {
    assertEq(4, 5);
  });
});
test("also skipped 2", () => {
  assertEq(6, 7);
});
console.log(77);
