function get(): i32 { return 0; }
function main(): i32 {
  console.log(get() ?? 8);
  return 0;
}
