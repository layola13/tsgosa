interface F { name: string; score: number; }
function main(): i32 {
  const f: F = { name: "ab", score: 9 };
  console.log(f.name);
  console.log(f.score);
  return 0;
}
