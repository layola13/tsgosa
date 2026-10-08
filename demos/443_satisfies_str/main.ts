function main(): i32 {
  const s = "ab" satisfies string;
  console.log(s);
  const n = 41 satisfies number;
  console.log(n + 1);
  return 0;
}
