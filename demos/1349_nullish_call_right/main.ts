function seven(): i32 {
  return 7;
}
function main(): i32 {
  const a: i32 | null = null;
  console.log(a ?? seven());
  return 0;
}
