function main(): i32 {
  console.log(/[0-9]+/.test("abc123") ? 1 : 0);
  console.log(/z+/.test("abc") ? 1 : 0);
  return 0;
}
