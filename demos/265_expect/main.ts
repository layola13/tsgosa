export function add(a: number, b: number): number {
  return a + b;
}
test("adds", () => {
  expect(add(1, 2)).toBe(3);
});
describe("more", () => {
  test("equal alias", () => {
    expect(add(2, 3)).toEqual(5);
  });
  test("strict", () => {
    expect(4).toStrictEqual(4);
  });
});
console.log(42);
