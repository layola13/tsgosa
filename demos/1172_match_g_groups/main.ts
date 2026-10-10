function main(): i32 {
  console.log("a1b2".match(/([0-9])/g).length);
  console.log("a1b2".match(/([0-9])/g)[1]);
  return 0;
}
