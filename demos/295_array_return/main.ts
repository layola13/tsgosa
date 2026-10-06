function get(): i32[] {
  return [1, 2];
}
function pair(): [i32, i32] {
  return [9, 10];
}
function main(): i32 {
  const a = get();
  const p = pair();
  console.log(a[0] + a[1] + p[0] + p[1]);
  return a[0] + a[1] + p[0] + p[1];
}
console.log(main());
