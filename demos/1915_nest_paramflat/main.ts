function get(a: i32[]): i32 {
  const [[x]] = a;
  return x;
}
function main(): i32 {
  const a: i32[] = [1];
  console.log(get(a));
  return 0;
}
