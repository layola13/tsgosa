function main(): i32 {
  console.log("ABC".match(/abc/i)[0] ?? "miss");
  console.log(/ABC/i.test("abc") ? 1 : 0);
  return 0;
}
