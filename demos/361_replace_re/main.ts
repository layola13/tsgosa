function main(): i32 {
  const s: string = "a1b2";
  console.log(s.replace(/[0-9]/, "N"));
  console.log(s.replace(/z/, "N"));
  console.log(s.replace("1", "N"));
  console.log(s.replaceAll(/[0-9]/g, "N"));
  return 0;
}
