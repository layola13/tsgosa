function get(): string { return "hey"; }
function main(): i32 {
  const f = get;
  console.log(f()?.length);
  return 0;
}
