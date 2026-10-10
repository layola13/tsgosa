function main(): i32 {
  const s: string = "hello";
  console.log(s.length > 3 ? s.slice(0, 3) : s);
  return 0;
}
