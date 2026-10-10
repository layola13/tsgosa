function get(): string {
  return "hey";
}
function main(): i32 {
  console.log(get()?.length);
  return 0;
}
