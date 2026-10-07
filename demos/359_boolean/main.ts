function main(): i32 {
  console.log(Boolean(0));
  console.log(Boolean(5));
  console.log(Boolean(""));
  const s: string = "a";
  console.log(Boolean(s));
  const m = new Map<string, number>();
  console.log(Boolean(m));
  console.log(Boolean(null));
  return 0;
}
