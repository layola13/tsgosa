function main(): i32 {
  const words: string[] = ["one", "two", "three"];
  console.log(words.map((w) => w.length).join(","));
  return 0;
}
