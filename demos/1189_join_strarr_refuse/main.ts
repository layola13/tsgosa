function main(): i32 {
  const m = "a1b2".match(/[0-9]/g);
  console.log(m.join("-"));
  return 0;
}
