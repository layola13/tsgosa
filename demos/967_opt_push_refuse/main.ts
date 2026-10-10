function main(): i32 {
  const a: i32[]|null = [1, 2];
  a?.push(3);
  console.log(a?.length ?? -1);
  return 0;
}
