let n: i32 = 0;
function bump(): i32 {
  n = n + 1;
  return 7;
}
function main(): i32 {
  const a: i32 | null = 3;
  console.log(a ?? bump());
  console.log(n);
  return 0;
}
