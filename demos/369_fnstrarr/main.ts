function get(): string[] {
  return ["x", "yy"];
}
function main(): i32 {
  console.log(get()[1]);
  console.log(get()[0]);
  const sv = get();
  console.log(sv[0]);
  console.log(sv[1]);
  return 0;
}
