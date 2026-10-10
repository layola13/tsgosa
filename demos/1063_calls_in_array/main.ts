function v(i: i32): i32 { return i * 10; }
function main(): i32 {
  const a: i32[] = [v(1), v(2), v(3)];
  console.log(a[0] + a[1] + a[2]);
  return 0;
}
