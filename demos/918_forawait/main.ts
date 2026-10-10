async function main(): Promise<i32> {
  for await (const v of [1, 2]) { console.log(v); }
  return 0;
}
