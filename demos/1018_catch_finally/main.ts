function main(): i32 {
  try {
    console.log(1);
  } catch (e) {
    console.log(2);
  } finally {
    console.log(3);
  }
  return 0;
}
