function swap(a: i32[], i: i32, j: i32): void { const t = a[i]; a[i] = a[j]; a[j] = t; }
function main(): i32 {
  const a = [1, 2];
  swap(a, 0, 1);
  console.log(a[0]);
  console.log(a[1]);
  return 0;
}
