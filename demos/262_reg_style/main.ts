export function add(a: number, b: number): number {
  return a + b;
}
export function assertEq(actual: number, expected: number): void {
  if (actual != expected) {
    throw new Error("no");
  }
}
describe("math", () => {
  test("adds", () => {
    const r = add(1, 2);
    assertEq(r, 3);
  });
  test("adds again", () => {
    const r = add(3, 4);
    assertEq(r, 7);
  });
  test.skip("broken", () => {
    assertEq(add(1, 1), 999);
  });
  test.todo("later");
  test("much later");
  it("eventually");
});
describe("focused", () => {
  test("skipped would fail", () => {
    assertEq(1, 2);
  });
  test.only("runs", () => {
    assertEq(2, 2);
  });
});
console.log(99);
