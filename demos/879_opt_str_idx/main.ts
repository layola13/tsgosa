function main(): i32 {
  const s: string|null = "hi";
  console.log(s?.[0]);
  console.log(s?.[1]);
  return 0;
}
