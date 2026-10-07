test("arr contain", () => {
  const a: i32[] = [4, 5, 6];
  expect(a).toContain(5);
  expect(a).not.toContain(9);
  expect([1, 2, 3]).toContain(3);
  expect([1, 2, 3]).not.toContain(0);
});
function main(): i32 {
  const b: i32[] = [7, 8];
  expect(b).toContain(8);
  return b.length;
}
console.log(main());
