function get(v: i32 | null): i32 {
  return v!;
}
function main(): i32 {
  console.log(get(5));
  return 0;
}
