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
  test("negation", () => {
    expect(add(1, 2)).not.toBe(4);
    expect(add(2, 3)).not.toEqual(6);
  });
  test("zero-arity", () => {
    expect(0).toBeNull();
    expect(0).toBeUndefined();
    expect(1).toBeTruthy();
    expect(0).toBeFalsy();
    expect(1).not.toBeNull();
    expect(0).not.toBeTruthy();
  });
});
console.log(42);
