function get(): string {
  return "hey";
}
function main(): i32 {
  console.log(get()?.[1]);
  return 0;
}
