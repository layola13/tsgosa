function main(): i32 {
  const a: i32|null = null;
  console.log(a === null ? 1 : 0);
  console.log(a !== null ? 1 : 0);
  console.log(null === null ? 1 : 0);
  return 0;
}
