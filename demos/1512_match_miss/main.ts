function main(): i32 {
  const m = "abc".match(/[0-9]+/);
  console.log(m == null ? 0 : m.length);
  return 0;
}
