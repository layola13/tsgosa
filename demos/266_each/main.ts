export function add(a: number, b: number): number {
  return a + b;
}
test.each([
  [1, 2, 3],
  [4, 5, 9],
])("adds", (a: number, b: number, expected: number) => {
  expect(add(a, b)).toBe(expected);
});
test.each([1, 2, 3])("positive", (n) => {
  expect(n).toBeGreaterThan(0);
});
it.each([
  ["hello", "ell"],
  ["world", "orl"],
])("contains", (s: string, sub: string) => {
  expect(s).toContain(sub);
});
test.each([])("empty", () => {
  expect(1).toBe(1);
});
console.log(11);
