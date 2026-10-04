function total(a: string, b: string): i32 {
  return a.length + b.length;
}
function main(): i32 {
  console.log(total("hello", "world"));
  return 0;
}