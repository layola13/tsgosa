function f(a: readonly i32[]): i32 {
  return a[0];
}
function main(): i32 {
  console.log(f([7, 8]));
  return 0;
}
