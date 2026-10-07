function main(): number { return 0; }
test("deep", () => {
  const a: number[] = [1, 2, 3];
  const b: number[] = [1, 2, 3];
  expect(a).toEqual(b);
  expect(a).toStrictEqual([1, 2, 3]);
  expect(a).toBe(a);
  expect(a).not.toEqual([1, 2, 4]);
  console.log(a.length + b.length);
});
test("streq", () => {
  const s: string[] = ["x", "y"];
  expect(s).toEqual(["x", "y"]);
  console.log(s.length);
});
