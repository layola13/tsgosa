function main(): i32 {
  const m = "ab12".match(/[0-9]+/);
  console.log(m.length);
  console.log(m[0]);
  return 0;
}
