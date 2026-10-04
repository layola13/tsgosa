async function get(): Promise<i32> {
  return 5;
}
function main(): i32 {
  const v: i32 = await get();
  console.log(v);
  return 0;
}
